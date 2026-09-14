export type WalletIdentityPhase = 'QUIESCING' | 'REBUILDING' | 'VERIFYING' | 'READY'

export interface WalletIdentityState {
  phase: WalletIdentityPhase
  generation: number
}

type IdentityListener = (state: Readonly<WalletIdentityState>) => void

let state: WalletIdentityState = { phase: 'READY', generation: 1 }
const listeners = new Set<IdentityListener>()
const CROSS_TAB_CHANNEL = 'sat20-wallet-identity-v1'
const CROSS_TAB_EVENT = 'wallet-identity-transition'
const seenCrossTabTransitions = new Set<string>()
let crossTabChannel: BroadcastChannel | null = null
let crossTabReloadScheduled = false

const emit = () => {
  const snapshot = { ...state }
  for (const listener of listeners) listener(snapshot)
}

const rememberCrossTabTransition = (token: string) => {
  seenCrossTabTransitions.add(token)
  if (seenCrossTabTransitions.size > 64) {
    const oldest = seenCrossTabTransitions.values().next().value
    if (oldest) seenCrossTabTransitions.delete(oldest)
  }
}

export const installWalletIdentityCrossTabBoundary = () => {
  if (crossTabChannel || typeof BroadcastChannel === 'undefined') return
  crossTabChannel = new BroadcastChannel(CROSS_TAB_CHANNEL)
  crossTabChannel.addEventListener('message', (event: MessageEvent) => {
    const message = event.data
    if (!message || message.type !== CROSS_TAB_EVENT ||
      typeof message.token !== 'string' || !/^[A-Za-z0-9_-]{16,128}$/.test(message.token) ||
      seenCrossTabTransitions.has(message.token)) return
    rememberCrossTabTransition(message.token)
    // This tab's WASM manager still holds the previous identity. Keep it
    // fail-closed after cancelling approvals and notifying DApps, then reload
    // so its store and WASM manager both initialize from the newly selected
    // shared wallet state. Updating only the visible Pinia fields would leave
    // signing bound to the previous WASM wallet.
    state = { phase: 'QUIESCING', generation: state.generation + 1 }
    emit()
    if (!crossTabReloadScheduled && typeof window !== 'undefined') {
      crossTabReloadScheduled = true
      window.setTimeout(() => window.location.reload(), 50)
    }
  })
}

export const getWalletIdentityState = (): Readonly<WalletIdentityState> => ({ ...state })

export const beginWalletIdentityTransition = (): number => {
  state = { phase: 'QUIESCING', generation: state.generation + 1 }
  emit()
  const token = crypto.randomUUID().replace(/-/g, '_')
  rememberCrossTabTransition(token)
  crossTabChannel?.postMessage({ type: CROSS_TAB_EVENT, token })
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
