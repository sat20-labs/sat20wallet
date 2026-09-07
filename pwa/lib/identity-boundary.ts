export type WalletIdentityPhase = 'QUIESCING' | 'REBUILDING' | 'VERIFYING' | 'READY'

export interface WalletIdentityState {
  phase: WalletIdentityPhase
  generation: number
}

type IdentityListener = (state: Readonly<WalletIdentityState>) => void

let state: WalletIdentityState = { phase: 'READY', generation: 1 }
const listeners = new Set<IdentityListener>()

const emit = () => {
  const snapshot = { ...state }
  for (const listener of listeners) listener(snapshot)
}

export const getWalletIdentityState = (): Readonly<WalletIdentityState> => ({ ...state })

export const beginWalletIdentityTransition = (): number => {
  state = { phase: 'QUIESCING', generation: state.generation + 1 }
  emit()
  return state.generation
}

export const setWalletIdentityPhase = (generation: number, phase: Exclude<WalletIdentityPhase, 'READY'>) => {
  if (state.generation !== generation) throw new Error('Stale wallet identity transition')
  state = { phase, generation }
  emit()
}

export const completeWalletIdentityTransition = (generation: number) => {
  if (state.generation !== generation) throw new Error('Stale wallet identity transition')
  state = { phase: 'READY', generation }
  emit()
}

export const assertWalletIdentityReady = (expectedGeneration?: number): number => {
  if (state.phase !== 'READY') {
    const error = new Error(`Wallet identity is ${state.phase.toLowerCase()}; retry after switching completes`)
    ;(error as Error & { status?: number; code?: string }).status = 503
    ;(error as Error & { status?: number; code?: string }).code = 'WALLET_IDENTITY_UNAVAILABLE'
    throw error
  }
  if (expectedGeneration !== undefined && expectedGeneration !== state.generation) {
    throw new Error('Wallet identity generation changed; approval is no longer valid')
  }
  return state.generation
}

export const subscribeWalletIdentity = (listener: IdentityListener) => {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

