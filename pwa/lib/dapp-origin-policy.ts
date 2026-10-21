import type { DappCapability } from './dapp-grant-model'

const PRODUCTION_DAPP_ORIGINS = [
  'https://app.ordx.market',
  'https://satsnet.ordx.market',
  'https://test-satsnet.ordx.market',
]

const isLoopbackHost = (hostname: string) =>
  hostname === 'localhost' || hostname === '127.0.0.1' || hostname === '[::1]' || hostname === '::1'

export interface DappOriginPolicyOptions {
  development: boolean
  test: boolean
  regtest?: boolean
  configured?: string
  currentProtocol: string
  currentHostname: string
}

export const getAllowedDappOrigins = (options: DappOriginPolicyOptions) => {
  const allowDevelopmentOrigins = options.development || options.test || options.regtest
  const result = new Set(options.regtest ? [] : PRODUCTION_DAPP_ORIGINS)
  const configured = options.configured?.split(',').map((value) => value.trim()).filter(Boolean) ?? []

  for (const candidate of configured) {
    try {
      const url = new URL(candidate)
      const allowedProtocol = options.regtest
        ? url.protocol === 'http:' && isLoopbackHost(url.hostname)
        : url.protocol === 'https:' ||
        (allowDevelopmentOrigins && url.protocol === 'http:' && isLoopbackHost(url.hostname))
      if (candidate === url.origin && allowedProtocol) result.add(url.origin)
    } catch {
      // Invalid configured origins never become wildcard trust.
    }
  }

  if (!options.regtest && allowDevelopmentOrigins && isLoopbackHost(options.currentHostname)) {
    ;[3001, 3006, 3007, 5173].forEach((port) => {
      result.add(`${options.currentProtocol}//${options.currentHostname}:${port}`)
    })
  }
  return result
}

const L1_MARKET_CAPABILITIES: readonly DappCapability[] = [
  'accounts:read', 'public-key:read', 'network:read', 'network:switch',
  'transaction:prepare', 'transaction:sign', 'transaction:broadcast', 'utxo:read',
]
const L2_MARKET_CAPABILITIES: readonly DappCapability[] = [
  ...L1_MARKET_CAPABILITIES,
  'balance:read', 'utxo:lock', 'utxo:unlock',
  'asset:send', 'contract:read', 'contract:write', 'identity:read', 'identity:write',
]

// Permission to load a DApp is broader than first-party Market membership.
export const getEmbeddedMarketCapabilities = (
  origin: string,
  options: DappOriginPolicyOptions,
): readonly DappCapability[] | null => {
  if (!getAllowedDappOrigins(options).has(origin)) return null
  if (!options.regtest && PRODUCTION_DAPP_ORIGINS.includes(origin)) {
    return origin === PRODUCTION_DAPP_ORIGINS[0] ? L1_MARKET_CAPABILITIES : L2_MARKET_CAPABILITIES
  }
  if (!options.development && !options.test && !options.regtest) return null
  try {
    const url = new URL(origin)
    if (url.protocol !== 'http:' || !isLoopbackHost(url.hostname)) return null
    if (url.port === '3001') return L1_MARKET_CAPABILITIES
    if (url.port === '3006') return L2_MARKET_CAPABILITIES
  } catch {
    // A noncanonical origin cannot become an embedded Market.
  }
  return null
}
