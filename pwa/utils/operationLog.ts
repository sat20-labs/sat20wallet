export type OperationLogStatus = 'pending' | 'running' | 'succeeded' | 'failed' | 'cancelled'

export interface OperationLogEvent {
  timestamp: number
  status?: OperationLogStatus
  message: string
  details?: Record<string, string>
}

export interface OperationLogRecord {
  id: string
  wallet_id?: number
  account_index?: number
  category: string
  action: string
  title: string
  summary: string
  status: OperationLogStatus
  created_at: number
  updated_at: number
  reservation_id?: number
  reservation_type?: string
  txid?: string
  parameters?: Record<string, string>
  result?: Record<string, string>
  history: OperationLogEvent[]
}

export interface OperationLogCreateInput {
  category: string
  action: string
  title: string
  summary: string
  parameters?: Record<string, string>
}

export interface OperationLogUpdateInput {
  status?: OperationLogStatus
  message: string
  details?: Record<string, string>
  result?: Record<string, string>
  txid?: string
}

// Display-only projection. Delivery/ACK alone never proves settlement.
export function projectSettledRGB11OperationLogs(records: OperationLogRecord[], transfers: any[]): OperationLogRecord[] {
  return records.map(record => {
    if (record.action !== 'rgb11_send_address' || !['pending', 'running'].includes(record.status)) return record
    const assetKey = record.parameters?.asset || ''
    if (!/^rgb11:[^:]+:[0-9a-f]{64}$/.test(assetKey)) return record
    const transferID = [...record.history].reverse().find(event => event.details?.transfer_id)?.details?.transfer_id
    if (!transferID) return record
    const matches = transfers.filter(transfer => {
      const name = transfer?.asset?.Name
      return transfer?.transfer_id === transferID && transfer.direction === 'send' &&
        `${name?.Protocol}:${name?.Type}:${name?.Ticker}` === assetKey
    })
    if (matches.length !== 1) return record
    const transfer = matches[0]
    if (transfer.status !== 'settled' || !/^[0-9a-f]{64}$/.test(transfer.witness_txid || '')) return record
    return {
      ...record,
      status: 'succeeded',
      summary: 'RGB address transfer settled (from current RGB transfer state)',
      txid: transfer.witness_txid,
      result: { ...record.result, transfer_id: transferID, witness_txid: transfer.witness_txid,
        status_source: 'Current RGB transfer state' },
    }
  })
}

export interface RGB11OperationStatus {
  transfer_id: string
  status: string
  ack_status: string
  delivery_record_key: string
  delivery_record_hash: string
  delivery_acknowledged: boolean | null
  witness_txid: string
  reject_reason: string
  logged_error: string
}

// A display-only whitelist, bound to the current account and one exact transfer.
// Never return the full RGB state: it also contains invoices and seal disclosures.
export function projectRGB11OperationStatus(
  record: OperationLogRecord,
  transfers: any[],
  identity: { walletId: string | number | undefined; accountIndex: string | number | undefined },
): RGB11OperationStatus | null {
  if (record.action !== 'rgb11_send_address' || record.wallet_id == null ||
    identity.walletId == null || identity.accountIndex == null ||
    String(record.wallet_id) !== String(identity.walletId) ||
    // The SDK JSON omits account_index for Root (Go uint32 zero + omitempty).
    String(record.account_index ?? 0) !== String(identity.accountIndex)) return null
  const assetKey = record.parameters?.asset || ''
  if (!/^rgb11:[^:]+:[0-9a-f]{64}$/.test(assetKey)) return null
  const ids = new Set([
    record.result?.transfer_id,
    ...record.history.map(event => event.details?.transfer_id),
  ].filter((value): value is string => typeof value === 'string' && value.length > 0))
  if (ids.size !== 1) return null
  const [transferID] = ids
  const matches = transfers.filter(transfer => {
    const name = transfer?.asset?.Name
    return transfer?.transfer_id === transferID && transfer.direction === 'send' &&
      `${name?.Protocol}:${name?.Type}:${name?.Ticker}` === assetKey
  })
  if (matches.length !== 1) return null
  const transfer = matches[0]
  const text = (value: unknown) => typeof value === 'string' ? value : ''
  return {
    transfer_id: transferID,
    status: text(transfer.status),
    ack_status: text(transfer.ack_status),
    delivery_record_key: text(transfer.delivery_record_key),
    delivery_record_hash: text(transfer.delivery_record_hash),
    delivery_acknowledged: typeof transfer.delivery_acknowledged === 'boolean' ? transfer.delivery_acknowledged : null,
    witness_txid: text(transfer.witness_txid),
    reject_reason: text(transfer.reject_reason),
    logged_error: text([...record.history].reverse().find(event => event.details?.error)?.details?.error),
  }
}

type WasmResult = {
  code: number
  msg?: string
  data?: Record<string, unknown>
}

function moduleApi(): any {
  return (globalThis as any).sat20wallet_operation_log
}

async function invoke(method: string, ...args: unknown[]): Promise<[Error | null, Record<string, unknown> | null]> {
  const api = moduleApi()
  if (!api || typeof api[method] !== 'function') {
    return [new Error('Operation log module is unavailable'), null]
  }
  try {
    const result = await api[method](...args) as WasmResult
    if (!result || result.code !== 0) {
      return [new Error(result?.msg || `${method} failed`), null]
    }
    return [null, result.data || null]
  } catch (error) {
    return [error instanceof Error ? error : new Error(String(error)), null]
  }
}

export async function beginOperationLog(input: OperationLogCreateInput): Promise<[Error | null, string | null]> {
  const [error, data] = await invoke('beginOperationLog', JSON.stringify(input))
  if (error) return [error, null]
  const id = typeof data?.id === 'string' ? data.id : ''
  return id ? [null, id] : [new Error('Operation log id is missing'), null]
}

export async function updateOperationLog(id: string, input: OperationLogUpdateInput): Promise<Error | null> {
  if (!id) return null
  const [error] = await invoke('updateOperationLog', id, JSON.stringify(input))
  return error
}

export async function getOperationLogs(): Promise<[Error | null, OperationLogRecord[]]> {
  const [error, data] = await invoke('getOperationLogs')
  if (error) return [error, []]
  try {
    const raw = typeof data?.logs === 'string' ? data.logs : '[]'
    return [null, JSON.parse(raw) as OperationLogRecord[]]
  } catch (parseError) {
    return [parseError instanceof Error ? parseError : new Error(String(parseError)), []]
  }
}

export async function getOperationLog(id: string): Promise<[Error | null, OperationLogRecord | null]> {
  const [error, data] = await invoke('getOperationLog', id)
  if (error) return [error, null]
  try {
    const raw = typeof data?.log === 'string' ? data.log : ''
    if (!raw) return [new Error('Operation log not found'), null]
    return [null, JSON.parse(raw) as OperationLogRecord]
  } catch (parseError) {
    return [parseError instanceof Error ? parseError : new Error(String(parseError)), null]
  }
}

export async function deleteAllOperationLogs(): Promise<Error | null> {
  const [error] = await invoke('deleteAllOperationLogs')
  return error
}
