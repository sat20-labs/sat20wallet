import { walletRequestSessionGuard } from '@/lib/walletSession'
import type { OperationView } from './walletViews'
async function invoke<T>(method: string): Promise<T> {
  const check = walletRequestSessionGuard(method)
  check()
  const result = await (globalThis as any).sat20wallet_operation_log[method]()
  check()
  if (!result || result.code !== 0) throw new Error(result?.msg || `${method} failed`)
  return result.data as T
}
export const getOperationLogs = () => invoke<OperationView[]>('getOperationLogs')
export const deleteOperationHistory = () => invoke<void>('deleteOperationHistory')
