export type OperationLogStatus = 'pending' | 'running' | 'succeeded' | 'failed' | 'cancelled'

export interface OperationLogEvent {
  timestamp: number
  status?: OperationLogStatus
  message: string
  details?: Record<string, string>
}

export interface OperationLogRecord {
  id: string
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
