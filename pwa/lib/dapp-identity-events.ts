import { canonicalOrigin, type DappGrantScope } from './dapp-grant-model'
import { snapshotCurrentDappGrantTargets } from './authorized-origins'
import { getWalletIdentityState, subscribeWalletIdentity } from './identity-boundary'

const EVENT_TTL_MS = 60_000

interface PendingDisconnect {
  origin: string
  oldScope: DappGrantScope
  transitionGeneration: number
  expiresAt: number
}

type DisconnectSink = (payload: { reason: 'wallet_scope_changed' }) => void

const pending = new Map<string, PendingDisconnect>()
const readySinks = new Map<string, DisconnectSink>()
let installed = false
let lastTransitionGeneration = 0

const eventKey = (event: PendingDisconnect) =>
  `${event.origin}\0${event.oldScope.network}\0${event.oldScope.walletFingerprint}\0${event.oldScope.accountIndex}\0${event.oldScope.identityGeneration}\0${event.transitionGeneration}`

const prune = (now = Date.now()) => {
  for (const [key, event] of pending) {
    if (event.expiresAt <= now || event.transitionGeneration < getWalletIdentityState().generation) pending.delete(key)
  }
}

export const installDappIdentityEventCoordinator = () => {
  if (installed) return
  installed = true
  subscribeWalletIdentity((identity) => {
    if (identity.phase !== 'QUIESCING' || identity.generation === lastTransitionGeneration) return
    lastTransitionGeneration = identity.generation
    const transitionGeneration = identity.generation
    const createdAt = Date.now()

    void snapshotCurrentDappGrantTargets(transitionGeneration - 1, createdAt).then((targets) => {
      if (getWalletIdentityState().generation !== transitionGeneration) return
      prune(createdAt)
      for (const target of targets) {
        const event: PendingDisconnect = {
          origin: target.origin,
          oldScope: target.scope,
          transitionGeneration,
          expiresAt: createdAt + EVENT_TTL_MS,
        }
        const sink = readySinks.get(target.origin)
        if (sink) sink({ reason: 'wallet_scope_changed' })
        else pending.set(eventKey(event), event)
      }
    }).catch((error) => {
      console.warn('Failed to queue DApp identity event:', error)
    })
  })
}

export const registerReadyDappIdentitySink = (origin: string, sink: DisconnectSink): (() => void) => {
  const exactOrigin = canonicalOrigin(origin)
  if (!exactOrigin) return () => {}
  readySinks.set(exactOrigin, sink)
  return () => {
    if (readySinks.get(exactOrigin) === sink) readySinks.delete(exactOrigin)
  }
}

export const consumePendingDappIdentityDisconnect = (origin: string, now = Date.now()): boolean => {
  const exactOrigin = canonicalOrigin(origin)
  if (!exactOrigin) return false
  prune(now)
  const generation = getWalletIdentityState().generation
  const match = [...pending.entries()].find(([, event]) =>
    event.origin === exactOrigin && event.transitionGeneration === generation && event.expiresAt > now)
  if (!match) return false
  pending.delete(match[0])
  return true
}
