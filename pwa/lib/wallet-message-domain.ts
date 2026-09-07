export const SAT20_WALLET_MESSAGE_DOMAIN = 'SAT20 Wallet Message'

export interface WalletMessageRequest {
  network: string
  origin: string
  timestamp: string
  nonce: string
  message: string
}

const hex = (bytes: Uint8Array) => [...bytes].map((value) => value.toString(16).padStart(2, '0')).join('')

export const buildWalletMessagePayload = (request: WalletMessageRequest, now = Date.now()) => {
  if (!request.network || /[\r\n]/.test(request.network)) throw new Error('Invalid wallet message network')
  let origin: string
  try {
    const parsed = new URL(request.origin)
    if (parsed.origin !== request.origin || !['http:', 'https:'].includes(parsed.protocol)) throw new Error()
    origin = parsed.origin
  } catch {
    throw new Error('Invalid wallet message origin')
  }
  if (!/^(0|[1-9][0-9]*)$/.test(request.timestamp)) throw new Error('Invalid wallet message timestamp')
  const timestamp = BigInt(request.timestamp)
  if (timestamp > BigInt(Number.MAX_SAFE_INTEGER)) throw new Error('Wallet message timestamp is out of range')
  if (Math.abs(now - Number(timestamp)) > 5 * 60_000) throw new Error('Wallet message timestamp is outside the allowed window')
  if (!/^[A-Za-z0-9_-]{16,128}$/.test(request.nonce)) throw new Error('Invalid wallet message nonce')
  if (typeof request.message !== 'string' || request.message.length === 0) throw new Error('Wallet message is required')
  return [
    SAT20_WALLET_MESSAGE_DOMAIN,
    'version:1',
    `network:${request.network}`,
    `origin:${origin}`,
    `timestamp:${request.timestamp}`,
    `nonce:${request.nonce}`,
    `message-length:${new TextEncoder().encode(request.message).length}`,
    '',
    request.message,
  ].join('\n')
}

export const walletMessageDigest = async (payload: string) => {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(payload))
  return hex(new Uint8Array(digest))
}

