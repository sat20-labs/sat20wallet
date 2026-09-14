// Temporary opt-in diagnostics. No parameters, results, errors or identity data.
// Disabled in normal builds; remove this module and its sat20.ts calls after J01.
type Phase = 'begin-log' | 'dispatch' | 'returned' | 'finish-log' | 'finished' | 'failure-log' | 'failed'
let sequence = 0
export function beginWalletAwaitTrace(method: string): (phase: Phase) => void {
  if (import.meta.env.VITE_WALLET_AWAIT_TRACE !== '1' || !['unlockWallet', 'validateMnemonic', 'importWallet', 'recoverAccountManagementFromRootMnemonic'].includes(method)) return () => {}
  const request = ++sequence, started = Date.now()
  return (phase) => {
    try { window.dispatchEvent(new CustomEvent('sat20:wallet-await-stage', { detail: { request, method, phase, elapsedMs: Date.now() - started } })) } catch { /* Observability cannot affect wallet control flow. */ }
  }
}
