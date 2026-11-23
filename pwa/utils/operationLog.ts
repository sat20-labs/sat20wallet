import { walletRequestSessionGuard } from '@/lib/walletSession'
import type { ContractLogResult, OperationView } from './walletViews'
async function invoke<T>(method: string, ...args: string[]): Promise<T> {
  const check = walletRequestSessionGuard(method)
  check()
  const result = await (globalThis as any).sat20wallet_operation_log[method](...args)
  check()
  if (!result || result.code !== 0) throw new Error(result?.msg || `${method} failed`)
  return result.data as T
}
export const getOperationLogs = () => invoke<OperationView[]>('getOperationLogs')
export const getContractLogResult = (id: string) => invoke<ContractLogResult>('getOperationLogs', id)
export const deleteOperationHistory = () => invoke<void>('deleteOperationHistory')
