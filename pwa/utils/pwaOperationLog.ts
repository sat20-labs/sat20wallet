// Existing log ownership retained only for lib/service.ts L1 execution entries.
interface OperationLogCreateInput { category: string; action: string; title: string; summary: string; parameters?: Record<string,string> }
interface OperationLogUpdateInput { status: string; message: string; details?: Record<string,string>; result?: Record<string,string>; txid?: string }
interface PwaOperationContext { id: string; title: string; successMessage: string }
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

async function beginOperationLog(input: OperationLogCreateInput): Promise<[Error | null, string | null]> {
  const [error, data] = await invoke('beginOperationLog', JSON.stringify(input))
  if (error) return [error, null]
  const id = typeof data?.id === 'string' ? data.id : ''
  return id ? [null, id] : [new Error('Operation log id is missing'), null]
}

async function updateOperationLog(id: string, input: OperationLogUpdateInput): Promise<Error | null> {
  if (!id) return null
  const [error] = await invoke('updateOperationLog', id, JSON.stringify(input))
  return error
}


function text(value: unknown): string {
  if (value === undefined || value === null) return ''
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean' || typeof value === 'bigint') return String(value)
  return ''
}

function safeRecord(values: Record<string, unknown>): Record<string, string> {
  const result: Record<string, string> = {}
  for (const [key, value] of Object.entries(values)) {
    const rendered = text(value).trim()
    if (rendered) result[key] = rendered
  }
  return result
}

function extractTxID(data: any): string {
  if (typeof data === 'string' && /^[0-9a-fA-F]{64}$/.test(data)) return data
  if (!data || typeof data !== 'object') return ''
  for (const key of ['txId', 'txid', 'tx_id', 'transaction_id', 'commitTxId', 'revealTxId']) {
    const value = text(data[key]).trim()
    if (value) return value
  }
  return ''
}

function extractSafeResult(data: any): Record<string, string> | undefined {
  if (!data || typeof data !== 'object') return undefined
  const result: Record<string, unknown> = {}
  for (const key of [
    'walletId', 'txId', 'txid', 'transaction_id', 'commitTxId', 'revealTxId', 'resvId', 'reservationId',
    'transfer_id', 'transfer_ids', 'request_id', 'contractAddress', 'contractType', 'orderId',
  ]) {
    const value = data[key]
    if (Array.isArray(value)) result[key] = value.length
    else if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') result[key] = value
  }
  const safe = safeRecord(result)
  return Object.keys(safe).length ? safe : undefined
}

export async function beginNamedPwaOperation(input: {
  category: string
  action: string
  title: string
  summary: string
  parameters?: Record<string, string>
  successMessage?: string
}): Promise<PwaOperationContext | null> {
  try {
    const [error, id] = await beginOperationLog({
      category: input.category,
      action: input.action,
      title: input.title,
      summary: input.summary,
      parameters: input.parameters,
    })
    if (error || !id) {
      if (error) console.warn('Unable to create operation log:', error)
      return null
    }
    return {
      id,
      title: input.title,
      successMessage: input.successMessage || `${input.title} completed`,
    }
  } catch (error) {
    console.warn('Unable to create operation log:', error)
    return null
  }
}


export async function finishPwaOperation(
  context: PwaOperationContext | null,
  error?: Error | null,
  data?: any,
): Promise<void> {
  if (!context) return
  try {
    if (error) {
      const pending = (error as any).data
      if (pending?.pending && pending?.transaction_id) {
        await updateOperationLog(context.id, { status: 'pending', message: error.message, txid: pending.transaction_id })
        return
      }
      await updateOperationLog(context.id, {
        status: 'failed',
        message: `${context.title} failed`,
        details: { error: error.message },
      })
      return
    }
    const txid = extractTxID(data)
    await updateOperationLog(context.id, {
      status: 'succeeded',
      message: context.successMessage,
      txid: txid || undefined,
      result: extractSafeResult(data),
    })
  } catch (logError) {
    console.warn('Unable to update operation log:', logError)
  }
}
