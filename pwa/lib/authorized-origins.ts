import { Storage } from './storage-adapter'
import { walletStorage } from './walletStorage'
import {
  DAPP_GRANT_SCHEMA_VERSION,
  canonicalOrigin,
  grantMatchesScope,
  isValidDappGrant,
  normalizeCapabilities,
  parseDappGrantEnvelope,
  removeMatchingDappGrants,
  type DappCapability,
  type DappGrant,
  type DappGrantScope,
} from './dapp-grant-model'
import { assertWalletIdentityReady } from './identity-boundary'

const DAPP_GRANTS_KEY = 'local:dapp_grants_v2'
const sessionGrants = new Map<string, DappGrant>()

const fingerprint = async (walletId: string, publicKey: string): Promise<string> => {
  const bytes = new TextEncoder().encode(`${walletId}\0${publicKey}`)
  const hash = await crypto.subtle.digest('SHA-256', bytes)
  return [...new Uint8Array(hash)].map((value) => value.toString(16).padStart(2, '0')).join('')
}

const readPersistentGrants = async (): Promise<DappGrant[]> => {
  const { value } = await Storage.get({ key: DAPP_GRANTS_KEY })
  const envelope = parseDappGrantEnvelope(value)
  return envelope?.grants ?? []
}

const writePersistentGrants = async (grants: DappGrant[]): Promise<void> => {
  await Storage.set({
    key: DAPP_GRANTS_KEY,
    value: JSON.stringify({ version: DAPP_GRANT_SCHEMA_VERSION, grants }),
  })
}

const grantKey = (grant: Pick<DappGrant, 'origin' | 'network' | 'walletFingerprint' | 'accountIndex' | 'identityGeneration'>) =>
  `${grant.origin}\0${grant.network}\0${grant.walletFingerprint}\0${grant.accountIndex}\0${grant.identityGeneration}`

export interface DappGrantTarget {
  origin: string
  scope: DappGrantScope
}

// Called synchronously from the identity transition listener. Copy session
// grants and wallet state before the transition can clear or replace them.
export const snapshotCurrentDappGrantTargets = (identityGeneration: number, now = Date.now()): Promise<DappGrantTarget[]> => {
  const state = walletStorage.getState()
  const sessionSnapshot = [...sessionGrants.values()]
  if (state.locked || !state.walletId || !state.pubkey) return Promise.resolve([])

  const network = state.network
  const accountIndex = Number(state.accountIndex)
  const persistentSnapshot = readPersistentGrants()
  return Promise.all([fingerprint(state.walletId, state.pubkey), persistentSnapshot]).then(([walletFingerprint, persistent]) => {
    const scope = { network, walletFingerprint, accountIndex, identityGeneration }
    const origins = new Set(
      [...sessionSnapshot, ...persistent]
        .filter((grant) => grant.network === scope.network &&
          grant.walletFingerprint === scope.walletFingerprint &&
          grant.accountIndex === scope.accountIndex &&
          grant.identityGeneration === scope.identityGeneration &&
          (grant.expiresAt === undefined || grant.expiresAt > now))
        .map((grant) => grant.origin)
    )
    return [...origins].map((origin) => ({ origin, scope }))
  })
}

export const getCurrentDappScope = async (): Promise<DappGrantScope> => {
	const identityGeneration = assertWalletIdentityReady()
  await walletStorage.initializeState()
  const state = walletStorage.getState()
  if (state.locked || !state.walletId || !state.pubkey) throw new Error('Wallet must be unlocked before granting DApp access')
  return {
    network: state.network,
    walletFingerprint: await fingerprint(state.walletId, state.pubkey),
    accountIndex: state.accountIndex,
	identityGeneration,
  }
}

export const createDappGrant = async (grant: DappGrant, assertApprovalValid: () => void = () => {}): Promise<void> => {
  if (!isValidDappGrant(grant)) throw new Error('Invalid DApp grant')
  const capabilities = normalizeCapabilities(grant.capabilities)
  if (!capabilities) throw new Error('Invalid DApp grant capabilities')
  const normalized = { ...grant, origin: canonicalOrigin(grant.origin)!, capabilities }
  const key = grantKey(normalized)
  assertApprovalValid()
  if (normalized.sessionOnly) {
    sessionGrants.set(key, normalized)
    return
  }
  const grants = (await readPersistentGrants()).filter((item) => grantKey(item) !== key)
  grants.push(normalized)
  assertApprovalValid()
  await writePersistentGrants(grants)
}

export const getMatchingDappGrant = async (
  origin: string,
  scope: DappGrantScope,
  capability: DappCapability,
  now = Date.now(),
): Promise<DappGrant | null> => {
  const exactOrigin = canonicalOrigin(origin)
  if (!exactOrigin) return null
  const grants = [...sessionGrants.values(), ...(await readPersistentGrants())]
  return grants.find((grant) => grantMatchesScope(grant, exactOrigin, scope, capability, now)) ?? null
}

export const isDappCapabilityGranted = async (
  origin: string,
  scope: DappGrantScope,
  capability: DappCapability,
) => Boolean(await getMatchingDappGrant(origin, scope, capability))

export const revokeDappGrant = async (origin: string, scope?: DappGrantScope): Promise<void> => {
  const exactOrigin = canonicalOrigin(origin)
  if (!exactOrigin) return
  const retainedSession = removeMatchingDappGrants([...sessionGrants.values()], exactOrigin, scope)
  sessionGrants.clear()
  for (const grant of retainedSession) sessionGrants.set(grantKey(grant), grant)
  await writePersistentGrants(removeMatchingDappGrants(await readPersistentGrants(), exactOrigin, scope))
}

export const clearSessionDappGrants = () => sessionGrants.clear()

export type { DappCapability, DappGrant, DappGrantScope }
