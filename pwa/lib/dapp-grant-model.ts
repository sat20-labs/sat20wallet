export const DAPP_GRANT_SCHEMA_VERSION = 3 as const

export const DAPP_CAPABILITIES = [
  'accounts:read',
  'public-key:read',
  'network:read',
  'network:switch',
  'balance:read',
  'utxo:read',
  'utxo:lock',
  'utxo:unlock',
  'transaction:prepare',
  'transaction:sign',
  'transaction:broadcast',
  'asset:send',
  'contract:read',
  'contract:write',
  'identity:read',
  'identity:write',
] as const

export type DappCapability = typeof DAPP_CAPABILITIES[number]

export interface DappGrantScope {
  network: string
  walletFingerprint: string
  accountIndex: number
  identityGeneration: number
}

export interface DappGrant extends DappGrantScope {
  origin: string
  createdAt: number
  expiresAt?: number
  capabilities: DappCapability[]
  sessionOnly: boolean
}

export interface DappGrantEnvelope {
  version: typeof DAPP_GRANT_SCHEMA_VERSION
  grants: DappGrant[]
}

const capabilitySet = new Set<string>(DAPP_CAPABILITIES)

export const canonicalOrigin = (value: string): string | null => {
  try {
    const url = new URL(value)
    if (url.origin !== value || !['http:', 'https:'].includes(url.protocol)) return null
    return url.origin
  } catch {
    return null
  }
}

export const normalizeCapabilities = (value: unknown): DappCapability[] | null => {
  if (!Array.isArray(value)) return null
  const result = [...new Set(value)]
  if (!result.length || result.some((item) => typeof item !== 'string' || !capabilitySet.has(item))) return null
  return result.sort() as DappCapability[]
}

export const isValidDappGrant = (value: unknown): value is DappGrant => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false
  const grant = value as Partial<DappGrant>
  return canonicalOrigin(String(grant.origin ?? '')) === grant.origin &&
    Number.isSafeInteger(grant.createdAt) && Number(grant.createdAt) > 0 &&
    (grant.expiresAt === undefined || (
      Number.isSafeInteger(grant.expiresAt) && Number(grant.expiresAt) > Number(grant.createdAt)
    )) &&
    typeof grant.network === 'string' && grant.network.length > 0 &&
    typeof grant.walletFingerprint === 'string' && /^[0-9a-f]{64}$/.test(grant.walletFingerprint) &&
    Number.isSafeInteger(grant.accountIndex) && Number(grant.accountIndex) >= 0 &&
    Number.isSafeInteger(grant.identityGeneration) && Number(grant.identityGeneration) > 0 &&
    normalizeCapabilities(grant.capabilities) !== null &&
    typeof grant.sessionOnly === 'boolean'
}

export const parseDappGrantEnvelope = (raw: string | null): DappGrantEnvelope | null => {
  if (!raw) return { version: DAPP_GRANT_SCHEMA_VERSION, grants: [] }
  try {
    const parsed = JSON.parse(raw) as Partial<DappGrantEnvelope>
    if (parsed.version !== DAPP_GRANT_SCHEMA_VERSION || !Array.isArray(parsed.grants) ||
      parsed.grants.some((grant) => !isValidDappGrant(grant))) return null
    return { version: DAPP_GRANT_SCHEMA_VERSION, grants: parsed.grants }
  } catch {
    return null
  }
}

export const grantMatchesScope = (
  grant: DappGrant,
  origin: string,
  scope: DappGrantScope,
  capability: DappCapability,
  now = Date.now(),
) => grant.origin === origin &&
  grant.network === scope.network &&
  grant.walletFingerprint === scope.walletFingerprint &&
  grant.accountIndex === scope.accountIndex &&
  grant.identityGeneration === scope.identityGeneration &&
  (grant.expiresAt === undefined || grant.expiresAt > now) &&
  grant.capabilities.includes(capability)

export const removeMatchingDappGrants = (
  grants: DappGrant[],
  origin: string,
  scope?: DappGrantScope,
) => grants.filter((grant) => !(grant.origin === origin && (!scope || (
  grant.network === scope.network &&
  grant.walletFingerprint === scope.walletFingerprint &&
    grant.accountIndex === scope.accountIndex
    && grant.identityGeneration === scope.identityGeneration
  ))))
