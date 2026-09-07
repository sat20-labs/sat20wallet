import { assertWalletIdentityReady } from './identity-boundary'
import { walletRequestSessionGuard } from './walletSession'

// This module retains only the UI handler, never a password or its result.
type PasswordPrompt = () => Promise<string | undefined>
let prompt: PasswordPrompt | undefined

export const registerWalletPasswordPrompt = (handler: PasswordPrompt) => {
  prompt = handler
  return () => { if (prompt === handler) prompt = undefined }
}

export const withWalletPassword = async <T>(operation: (password: string) => Promise<T>): Promise<T | undefined> => {
  const checkSession = walletRequestSessionGuard('confirmWalletPassword')
  assertWalletIdentityReady()
  if (!prompt) throw new Error('Wallet password dialog is unavailable')
  let password = await prompt()
  if (password === undefined) return undefined
  try {
    checkSession()
    return await operation(password)
  } finally {
    password = undefined
  }
}
