// These are manually exchanged matching data, never a signed or standard RGB ACK.
export interface RGB11PrebroadcastSummary {
  version: 1
  stage: 'validated-awaiting-broadcast'
  contract_id: string
  schema_id: string
  consignment_hash: string
  transfer_id: string
  witness_txid: string
  invoice_hash: string
  amount_raw: string
  precision: number
  recipient_outpoint: string
}

export const sha256Text = async (value: string): Promise<string> => {
  const hash = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(value))
  return Array.from(new Uint8Array(hash), (byte) => byte.toString(16).padStart(2, '0')).join('')
}

// Hash and submit exactly the same text received from the sender. Armor's
// leading/trailing whitespace is part of its object hash, even when the RGB
// decoder tolerates it. trim is only an emptiness/format check.
export const rgb11ConsignmentInput = (input: string): string => {
  let consignment = input
  if (input.trimStart().startsWith('{')) {
    const parsed = JSON.parse(input)
    if (parsed?.transport_mode !== 'out-of-band' || typeof parsed.consignment !== 'string') {
      throw new Error('Invalid RGB11 transfer package')
    }
    consignment = parsed.consignment
  }
  if (!consignment.trim()) throw new Error('Invalid RGB11 transfer package')
  return consignment
}

// Compatibility is confined to whitespace outside a complete RGB armor.
// ParseArmor stops at its END line; no header or encoded body is normalized.
const completeRGB11Armor = (text: string): boolean =>
  /^[ \t\r\n]*-----BEGIN RGB CONSIGNMENT-----\r?\n[\s\S]+\r?\n-----END RGB CONSIGNMENT-----[ \t\r\n]*$/.test(text)

const matchesRGB11ObjectHash = async (summaryHash: string, preparedHash: string, original: string): Promise<boolean> => {
  if (!preparedHash) return false
  if (summaryHash === preparedHash) return true
  if (!original || !completeRGB11Armor(original) || await sha256Text(original) !== preparedHash) return false
  return summaryHash === await sha256Text(original.trim())
}

export const rgb11ResumeConsignment = async (
  input: string, savedHash: string, summaryHash: string, prepared: boolean,
): Promise<string> => {
  const original = rgb11ConsignmentInput(input)
  const hash = await sha256Text(original)
  if (!savedHash || savedHash === hash) return original
  // Retain the exact bytes that the old PWA already validated, and retain its
  // hashes/ACK marker. New requests never enter this compatibility branch.
  if (prepared && summaryHash === savedHash && completeRGB11Armor(original) &&
      await sha256Text(original.trim()) === savedHash) return original.trim()
  throw new Error('Consignment differs from the validated original')
}

export const rgb11Schema = (state: any, contractID: string): string => {
  for (const info of state?.ticker_infos || []) {
    try {
      const content = info.content ?? info.Content
      const ext = typeof content === 'string' ? JSON.parse(atob(content)) : content
      if (ext?.contract_id === contractID && typeof ext.schema_id === 'string') return ext.schema_id
    } catch { /* An unrelated ticker cannot supply this contract's schema. */ }
  }
  return ''
}

export const rgb11Amount = (state: any) => {
  const amount = state?.asset?.Amount
  if (!/^[1-9]\d*$/.test(amount?.Value) || !Number.isInteger(amount?.Precision)) {
    throw new Error('Missing validated RGB amount')
  }
  return { amount_raw: String(amount.Value), precision: amount.Precision as number }
}

export const matchesRGB11Summary = async (
  text: string, state: any, contractID: string, schemaID: string, originalConsignment = '',
): Promise<boolean> => {
  try {
    const summary = JSON.parse(text) as RGB11PrebroadcastSummary
    const amount = rgb11Amount(state)
    const sameTransfer = summary.transfer_id === state.transfer_id || (
      !!state.batch_id && summary.transfer_id === state.batch_id && state.recipient_vout > 0 &&
      summary.recipient_outpoint === `${state.witness_txid}:${state.recipient_vout}`
    )
    return summary.version === 1 && summary.stage === 'validated-awaiting-broadcast' &&
      !!schemaID && summary.schema_id === schemaID && summary.contract_id === contractID &&
      await matchesRGB11ObjectHash(summary.consignment_hash, state.consignment_hash, originalConsignment) &&
      !!state.witness_txid && summary.witness_txid === state.witness_txid && sameTransfer &&
      (!state.recipient_vout || summary.recipient_outpoint === `${state.witness_txid}:${state.recipient_vout}`) &&
      !!state.invoice && summary.invoice_hash === await sha256Text(state.invoice) &&
      summary.amount_raw === amount.amount_raw && summary.precision === amount.precision
  } catch { return false }
}

// TickerInfo encodes its asset as `name`; TransferState.asset uses `Name`.
export const rgb11TaskResumeAsset = (state: any, transfer: any) => {
  const name = transfer?.asset?.Name
  if (!name?.Protocol || !name?.Type || !name?.Ticker) return null
  const info = (state?.ticker_infos || []).find((item: any) => {
    const asset = item.Name || item.name || item.AssetName || item.asset_name
    return asset?.Protocol === name.Protocol && asset?.Type === name.Type && asset?.Ticker === name.Ticker
  })
  const contractID = info?.contract_id || info?.ContractID
  return contractID ? { contract_id: contractID, ticker: name.Ticker, protocol: name.Protocol, type: name.Type } : null
}

export const rgb11ResumedPackageSetMatches = async (packages: any[], ids: string[]): Promise<boolean> => {
  if (!ids.length || packages.length !== ids.length || new Set(ids).size !== ids.length) return false
  const first = packages[0]
  const source = first?.recipient_consignment
  if (typeof source !== 'string' || !source || !first?.state?.witness_txid) return false
  const sourceHash = await sha256Text(source)
  const firstAsset = first.state.asset?.Name
  return packages.every((item, index) => {
    const state = item?.state
    const asset = state?.asset?.Name
    const memberIDs = state?.batch_transfer_ids?.length ? state.batch_transfer_ids : [state?.transfer_id]
    return state?.transfer_id === ids[index] && memberIDs.length === ids.length && memberIDs.every((id: string, i: number) => id === ids[i]) &&
      item.recipient_consignment === source && state.consignment_hash === sourceHash &&
      state.direction === 'send' && state.transport_mode === 'out-of-band' &&
      state.witness_txid === first.state.witness_txid && ['prepared', 'delivered', 'relayed'].includes(state.status) &&
      !!asset && !!firstAsset && asset.Protocol === firstAsset.Protocol && asset.Type === firstAsset.Type && asset.Ticker === firstAsset.Ticker
  })
}

// Session-only cache survives dialog unmounts; a page reload requires reselecting the original file.
export const rgb11ReceivePackages = new Map<string, string>()
