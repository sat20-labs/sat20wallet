export const STP_AGENT_VALUE_MOVEMENT_OPERATIONS = new Set([
  'stp.close',
  'stp.splicing_in',
  'stp.splicing_out',
  'stp.lock',
  'stp.lock_with_expand',
  'stp.unlock',
])

export const SAFE_PUNISH_COVERAGE_STATUSES = new Set([
  'NO_REVOKED_REMOTE_STATE',
  'COVERED',
])

export interface StpValueMovementSafetyAssessment {
  allowed: boolean
  reason?: string
  details?: Record<string, any>
}

const pick = (value: any, ...keys: string[]) => {
  for (const key of keys) {
    if (value && value[key] !== undefined && value[key] !== null) {
      return value[key]
    }
  }
  return undefined
}

const normalizedStatus = (value: any) => String(value || '').trim().toUpperCase()

export const isStpAgentValueMovementOperation = (operation: string) => (
  STP_AGENT_VALUE_MOVEMENT_OPERATIONS.has(operation)
)

export const assessStpValueMovementSafety = (snapshot: any): StpValueMovementSafetyAssessment => {
  const status = normalizedStatus(pick(snapshot, 'status', 'Status'))
  const punishCoverage = pick(snapshot, 'punish_coverage', 'punishCoverage', 'PunishCoverage') || {}
  const punishCoverageStatus = normalizedStatus(pick(punishCoverage, 'status', 'Status'))
  const details = {
    status: status || 'UNKNOWN',
    punish_coverage: punishCoverage,
    punish_coverage_status: punishCoverageStatus || 'UNKNOWN',
    missing_evidence: pick(snapshot, 'missing_evidence', 'missingEvidence', 'MissingEvidence') || [],
    next_check: pick(snapshot, 'next_check', 'nextCheck', 'NextCheck') || '',
  }

  if (status !== 'READY_SAFE') {
    return {
      allowed: false,
      reason: `STP safety snapshot is ${details.status}; value-moving Agent operations require READY_SAFE.`,
      details,
    }
  }

  if (!SAFE_PUNISH_COVERAGE_STATUSES.has(punishCoverageStatus)) {
    return {
      allowed: false,
      reason: `STP punish coverage is ${details.punish_coverage_status}; value-moving Agent operations require NO_REVOKED_REMOTE_STATE or COVERED.`,
      details,
    }
  }

  return {
    allowed: true,
    details,
  }
}
