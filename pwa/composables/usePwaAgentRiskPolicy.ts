export interface AgentRiskFlag {
  code: string
  label: string
  detail: string
  severity: 'info' | 'warning' | 'critical'
}

export interface AgentRiskAssessment {
  requiresSecondConfirmation: boolean
  flags: AgentRiskFlag[]
}

export interface AgentSignatureSummary {
  kind: 'structured' | 'raw'
  title: string
  rows: Array<{ key: string; value: string }>
  warning?: string
}

const HIGH_RISK_STP_OPERATIONS = new Set([
  'stp.close',
  'stp.splicing_in',
  'stp.splicing_out',
  'stp.lock',
  'stp.lock_with_expand',
  'stp.unlock',
])

const DEFAULT_LARGE_AMOUNT_THRESHOLD = 1_000_000

const text = (value: any) => String(value ?? '').trim()

const numberValue = (value: any) => {
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : 0
}

const flag = (
  code: string,
  label: string,
  detail: string,
  severity: AgentRiskFlag['severity'] = 'warning'
): AgentRiskFlag => ({
  code,
  label,
  detail,
  severity,
})

export const assessAgentOperationRisk = (
  operation: string,
  params: Record<string, any> = {}
): AgentRiskAssessment => {
  const flags: AgentRiskFlag[] = []

  if (operation === 'wallet.send_assets') {
    const destination = text(params.to ?? params.address ?? params.dest_addr)
    const amount = numberValue(params.amount ?? params.amount_sats)
    const chain = text(params.chain ?? params.layer ?? params.network).toLowerCase()
    const knownDestination = Boolean(params.known_destination || params.known_recipient || params.recipient_label)
    const threshold = numberValue(params.risk_threshold)
      || numberValue(params.large_amount_threshold)
      || DEFAULT_LARGE_AMOUNT_THRESHOLD

    flags.push(flag(
      'VALUE_MOVEMENT',
      'Value movement',
      'This request can move wallet assets.'
    ))

    if (destination && !knownDestination) {
      flags.push(flag(
        'UNKNOWN_DESTINATION',
        'Unknown destination',
        'The destination is not marked as a known recipient.',
        'critical'
      ))
    }
    if (amount >= threshold) {
      flags.push(flag(
        'LARGE_TRANSFER',
        'Large transfer',
        `The requested amount ${amount} meets or exceeds the review threshold ${threshold}.`,
        'critical'
      ))
    }
    if (chain.includes('mainnet') || params.mainnet === true) {
      flags.push(flag(
        'MAINNET_VALUE_MOVEMENT',
        'Mainnet operation',
        'This request targets mainnet value movement.',
        'critical'
      ))
    }
  } else if (HIGH_RISK_STP_OPERATIONS.has(operation)) {
    flags.push(flag(
      'STP_VALUE_MOVEMENT',
      'STP value movement',
      'This request can change STP channel state or move channel-protected assets.',
      'critical'
    ))
  } else if (['wallet.export_mnemonic', 'wallet.change_password', 'wallet.import', 'wallet.create'].includes(operation)) {
    flags.push(flag(
      'SECRET_OPERATION',
      'Secret operation',
      'This request can create, reveal, import, or change wallet secrets.',
      'critical'
    ))
  }

  const requiresSecondConfirmation = flags.some((item) => item.severity === 'critical')
  return { flags, requiresSecondConfirmation }
}

export const summarizeSignaturePayload = (payload: unknown): AgentSignatureSummary => {
  const raw = typeof payload === 'string' ? payload : JSON.stringify(payload ?? '')
  try {
    const parsed = typeof payload === 'string' ? JSON.parse(payload) : payload
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      throw new Error('not an object')
    }
    const value = parsed as Record<string, any>
    const rows = [
      ['domain', value.domain ?? value.origin ?? value.dapp ?? value.app],
      ['action', value.action ?? value.method ?? value.type],
      ['address', value.address ?? value.account ?? value.from],
      ['to', value.to ?? value.recipient ?? value.destination],
      ['asset', value.asset ?? value.assetName ?? value.ticker],
      ['amount', value.amount ?? value.amount_sats ?? value.value],
      ['expiry', value.expiry ?? value.expires_at ?? value.deadline],
      ['nonce', value.nonce],
    ]
      .filter(([, rowValue]) => rowValue !== undefined && rowValue !== null && String(rowValue) !== '')
      .map(([key, rowValue]) => ({ key: String(key), value: String(rowValue) }))

    return {
      kind: 'structured',
      title: 'Structured signature payload',
      rows,
    }
  } catch {
    return {
      kind: 'raw',
      title: 'Raw opaque signature',
      rows: [{ key: 'payload', value: raw }],
      warning: 'This signature payload is not recognized as structured SAT20 protocol data.',
    }
  }
}

export const stableAgentParamsHash = (operation: string, params: Record<string, any> = {}) => {
  const normalized = JSON.stringify({ operation, params }, Object.keys({ operation, params }).sort())
  let hash = 0
  for (let i = 0; i < normalized.length; i++) {
    hash = ((hash << 5) - hash + normalized.charCodeAt(i)) | 0
  }
  return Math.abs(hash).toString(16)
}
