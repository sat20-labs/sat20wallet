import { getWalletIdentityState } from './identity-boundary'

// UI authorization is memory-only and independent of the still-running SDK.
// Never retain credentials here or restore authorization from persisted state.
let walletSessionUnlocked = false
let walletRuntimeUnlocked = false

export const isWalletSessionUnlocked = () => walletSessionUnlocked

export const setWalletSessionUnlocked = (unlocked: boolean) => {
  walletSessionUnlocked = unlocked
}

export const isWalletRuntimeUnlocked = () => walletRuntimeUnlocked

export const setWalletRuntimeUnlocked = (unlocked: boolean) => {
  walletRuntimeUnlocked = unlocked
}

// Only initialization, password-authenticated entry points and the public
// identity reads needed by those entry points are available without a session.
// This guards the PWA facades, not direct access to the trusted-host WASM API.
const sessionIndependentMethods = new Set([
  'init', 'start', 'release', 'registerCallback', 'getVersion',
  'isWalletExist', 'isWalletExisting', 'getChain',
  'createWallet', 'importWallet', 'importWalletWithPrivKey', 'validateMnemonic',
  'recoverAccountManagementFromRootMnemonic', 'unlockWallet',
  'getAllWallets', 'getWalletCatalog', 'getWalletAddress', 'getWalletPubkey',
])

export const walletRequestSessionGuard = (method: string): (() => void) => {
  const generation = getWalletIdentityState().generation
  const check = () => {
    if (generation !== getWalletIdentityState().generation) {
      throw new Error('Wallet session changed; request is no longer valid')
    }
    if (!sessionIndependentMethods.has(method) && !walletSessionUnlocked) {
      throw new Error('Wallet must be unlocked')
    }
  }
  check()
  return check
}
