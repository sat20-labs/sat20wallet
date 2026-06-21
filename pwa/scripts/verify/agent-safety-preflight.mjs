import assert from 'node:assert/strict'
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { transform } from 'esbuild'

const compileModule = async (relativePath, outputName) => {
  const sourcePath = new URL(relativePath, import.meta.url)
  const source = await readFile(sourcePath, 'utf8')
  const { code } = await transform(source, {
    loader: 'ts',
    format: 'esm',
    target: 'es2022',
  })
  const modulePath = join(tempDir, outputName)
  await writeFile(modulePath, code)
  return import(pathToFileURL(modulePath))
}

const tempDir = await mkdtemp(join(tmpdir(), 'sat20-agent-safety-'))

try {
  const {
    assessStpValueMovementSafety,
    isStpAgentValueMovementOperation,
  } = await compileModule('../../composables/usePwaAgentAdapterSafety.ts', 'usePwaAgentAdapterSafety.mjs')
  const {
    assessAgentOperationRisk,
    summarizeSignaturePayload,
  } = await compileModule('../../composables/usePwaAgentRiskPolicy.ts', 'usePwaAgentRiskPolicy.mjs')

  assert.equal(isStpAgentValueMovementOperation('stp.unlock'), true)
  assert.equal(isStpAgentValueMovementOperation('stp.punish_broadcast'), false)
  assert.equal(isStpAgentValueMovementOperation('stp.safety_snapshot'), false)

  assert.equal(assessStpValueMovementSafety({
    status: 'READY_SAFE',
    punish_coverage: { status: 'NO_REVOKED_REMOTE_STATE' },
  }).allowed, true)

  assert.equal(assessStpValueMovementSafety({
    status: 'READY_SAFE',
    punish_coverage: { status: 'COVERED' },
  }).allowed, true)

  const unknownCoverage = assessStpValueMovementSafety({
    status: 'READY_SAFE',
    punish_coverage: {
      status: 'PUNISH_COVERAGE_UNKNOWN',
      missing: ['MISSING_PUNISH_COVERAGE'],
    },
  })
  assert.equal(unknownCoverage.allowed, false)
  assert.match(unknownCoverage.reason, /PUNISH_COVERAGE_UNKNOWN/)

  const missingCoverage = assessStpValueMovementSafety({
    status: 'READY_SAFE',
    punish_coverage: {
      status: 'PUNISH_COVERAGE_MISSING',
      missing: ['MISSING_PUNISH_COVERAGE'],
    },
  })
  assert.equal(missingCoverage.allowed, false)
  assert.match(missingCoverage.reason, /PUNISH_COVERAGE_MISSING/)

  const degraded = assessStpValueMovementSafety({
    status: 'READY_DEGRADED',
    punish_coverage: { status: 'COVERED' },
    missing_evidence: ['MISSING_LOCAL_COMMITMENT'],
  })
  assert.equal(degraded.allowed, false)
  assert.match(degraded.reason, /READY_DEGRADED/)

  const riskySend = assessAgentOperationRisk('wallet.send_assets', {
    to: 'bc1qunknown',
    asset: '::',
    amount_sats: 2_000_000,
    chain: 'mainnet',
  })
  assert.equal(riskySend.requiresSecondConfirmation, true)
  assert.deepEqual(
    riskySend.flags.map((item) => item.code),
    ['VALUE_MOVEMENT', 'UNKNOWN_DESTINATION', 'LARGE_TRANSFER', 'MAINNET_VALUE_MOVEMENT']
  )

  const readOnly = assessAgentOperationRisk('stp.safety_snapshot', {})
  assert.equal(readOnly.requiresSecondConfirmation, false)
  assert.equal(readOnly.flags.length, 0)

  const structuredSignature = summarizeSignaturePayload(JSON.stringify({
    domain: 'sat20.org',
    action: 'authorize',
    to: 'bc1qrecipient',
    amount: '1000',
    nonce: 'abc',
  }))
  assert.equal(structuredSignature.kind, 'structured')
  assert.equal(structuredSignature.rows.some((row) => row.key === 'action' && row.value === 'authorize'), true)

  const rawSignature = summarizeSignaturePayload('sign this opaque blob')
  assert.equal(rawSignature.kind, 'raw')
  assert.match(rawSignature.warning, /not recognized/)
} finally {
  await rm(tempDir, { recursive: true, force: true })
}
