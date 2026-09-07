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
  configured?: string
  currentProtocol: string
  currentHostname: string
}

export const getAllowedDappOrigins = (options: DappOriginPolicyOptions) => {
  const allowDevelopmentOrigins = options.development || options.test
  const result = new Set(PRODUCTION_DAPP_ORIGINS)
  const configured = options.configured?.split(',').map((value) => value.trim()).filter(Boolean) ?? []

  for (const candidate of configured) {
    try {
      const url = new URL(candidate)
      const allowedProtocol = url.protocol === 'https:' ||
        (allowDevelopmentOrigins && url.protocol === 'http:' && isLoopbackHost(url.hostname))
      if (candidate === url.origin && allowedProtocol) result.add(url.origin)
    } catch {
      // Invalid configured origins never become wildcard trust.
    }
  }

  if (allowDevelopmentOrigins && isLoopbackHost(options.currentHostname)) {
    ;[3001, 3006, 3007, 5173].forEach((port) => {
      result.add(`${options.currentProtocol}//${options.currentHostname}:${port}`)
    })
  }
  return result
}
