// Pure matching checks; no wallet, network, chain, broadcast or SDK state access.
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { webcrypto } from 'node:crypto'
import ts from 'typescript'
if (!globalThis.crypto) globalThis.crypto = webcrypto
const source = await readFile(new URL('../../utils/rgb11Oob.ts', import.meta.url), 'utf8')
const { outputText } = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } })
const { matchesRGB11Summary: matches, sha256Text, rgb11Schema, rgb11ConsignmentInput, rgb11ResumeConsignment, rgb11ResumedPackageSetMatches, rgb11TaskResumeAsset } = await import(`data:text/javascript;base64,${Buffer.from(outputText).toString('base64')}`)
const state = { transfer_id: 'single', invoice: 'invoice-one', consignment_hash: 'hash-one', witness_txid: 'tx-one',
  recipient_vout: 1, asset: { Amount: { Value: '5', Precision: 0 } } }
const summary = { version: 1, stage: 'validated-awaiting-broadcast', contract_id: 'rgb:contract', schema_id: 'schema',
  consignment_hash: state.consignment_hash, witness_txid: state.witness_txid, transfer_id: state.transfer_id,
  invoice_hash: await sha256Text(state.invoice), amount_raw: '5', precision: 0, recipient_outpoint: 'tx-one:1' }
const match = (value, target = state) => matches(JSON.stringify(value), target, 'rgb:contract', 'schema')
assert.equal(await match(summary), true)
for (const [field, value] of Object.entries({ version: 2, stage: 'accepted', contract_id: 'wrong', schema_id: 'wrong',
  consignment_hash: 'wrong', witness_txid: 'wrong', transfer_id: 'wrong', invoice_hash: 'wrong', amount_raw: '6', precision: 1,
  recipient_outpoint: 'tx-one:2' })) {
  assert.equal(await match({ ...summary, [field]: value }), false, field)
}
assert.equal(await matches('{}', state, 'rgb:contract', 'schema'), false)
assert.equal(await matches(JSON.stringify(summary), state, 'rgb:contract', ''), false)
const batchFirst = { ...state, transfer_id: 'child-one', batch_id: 'batch' }
assert.equal(await match({ ...summary, transfer_id: 'batch' }, batchFirst), true)
const batchSecond = { ...batchFirst, transfer_id: 'child-two', invoice: 'invoice-two', recipient_vout: 2 }
assert.equal(await match({ ...summary, transfer_id: 'batch' }, batchSecond), false, 'one recipient cannot approve another')
assert.equal(await match({ ...summary, transfer_id: 'batch', invoice_hash: await sha256Text(batchSecond.invoice),
  recipient_outpoint: 'tx-one:2' }, batchSecond), true)
assert.equal(rgb11Schema({ ticker_infos: [{ content: Buffer.from(JSON.stringify({ contract_id: 'rgb:contract', schema_id: 'schema' })).toString('base64') }] }, 'rgb:contract'), 'schema')
assert.equal(rgb11Schema({ ticker_infos: [{ content: 'invalid' }] }, 'rgb:contract'), '')
// A trailing LF changes the object hash. Both direct armor and the optional
// JSON envelope must preserve the exact submitted text, including CRLF.
for (const armor of ['-----BEGIN RGB-----\nexample\n-----END RGB-----\n', '\r\narmor\r\n']) {
  const senderHash = await sha256Text(armor)
  const direct = rgb11ConsignmentInput(armor)
  const wrapped = rgb11ConsignmentInput(JSON.stringify({ transport_mode: 'out-of-band', consignment: armor }))
  assert.equal(direct, armor)
  assert.equal(wrapped, armor)
  assert.equal(await sha256Text(direct), senderHash)
  assert.equal(await sha256Text(wrapped), senderHash)
  assert.notEqual(await sha256Text(direct.trim()), senderHash)
  assert.equal(await match({ ...summary, consignment_hash: senderHash }, { ...state, consignment_hash: senderHash }), true)
  assert.equal(await match({ ...summary, consignment_hash: await sha256Text(direct.trim()) }, { ...state, consignment_hash: senderHash }), false)
}
assert.throws(() => rgb11ConsignmentInput(' \n\t'))
assert.throws(() => rgb11ConsignmentInput(JSON.stringify({ transport_mode: 'other', consignment: 'armor' })))
// Old PWA trim compatibility requires the sender's exact SDK-bound source.
for (const newline of ['\n', '\r\n']) {
  const armor = ['-----BEGIN RGB CONSIGNMENT-----', 'Version: 1', '', 'same-body', '-----END RGB CONSIGNMENT-----', ''].join(newline)
  const rawHash = await sha256Text(armor)
  const oldHash = await sha256Text(armor.trim())
  const boundState = { ...state, consignment_hash: rawHash }
  const oldSummary = { ...summary, consignment_hash: oldHash }
  const check = (value = oldSummary, target = boundState, source = armor) => matches(JSON.stringify(value), target, 'rgb:contract', 'schema', source)
  assert.equal(await check(), true, 'old LF/CRLF trimmed receipt matches the same source')
  assert.equal(await check({ ...summary, consignment_hash: rawHash }), true, 'exact hash remains accepted')
  assert.equal(await check(oldSummary, boundState, armor.replace('same-body', 'changed-body')), false, 'source hash must bind to SDK state')
  assert.equal(await check({ ...oldSummary, consignment_hash: await sha256Text(armor.trim().replace('same-body', 'changed-body')) }), false, 'body changes are not whitespace compatibility')
  assert.equal(await check({ ...oldSummary, invoice_hash: await sha256Text('other-invoice') }), false)
  assert.equal(await check(oldSummary, { ...boundState, consignment_hash: 'unrelated' }), false)
  assert.equal(await rgb11ResumeConsignment(armor, oldHash, oldHash, true), armor.trim())
  assert.equal(await rgb11ResumeConsignment(armor, rawHash, rawHash, true), armor)
  assert.equal(await rgb11ResumeConsignment(armor, '', '', false), armor, 'new request retains bytes')
  await assert.rejects(rgb11ResumeConsignment(armor, oldHash, 'other', true))
  await assert.rejects(rgb11ResumeConsignment(armor, oldHash, oldHash, false))
  await assert.rejects(rgb11ResumeConsignment(armor.replace('same-body', 'changed-body'), oldHash, oldHash, true))
}
const nonArmor = 'not-armor\n'
const nonArmorHash = await sha256Text(nonArmor)
const nonArmorTrimHash = await sha256Text(nonArmor.trim())
assert.equal(await matches(JSON.stringify({ ...summary, consignment_hash: nonArmorTrimHash }),
  { ...state, consignment_hash: nonArmorHash }, 'rgb:contract', 'schema', nonArmor), false)
await assert.rejects(rgb11ResumeConsignment(nonArmor, nonArmorTrimHash, nonArmorTrimHash, true))
const resumeSource = 'sdk-package'
const resumeIDs = ['resume-one', 'resume-two']
const resumePackages = await Promise.all(resumeIDs.map(async (id) => ({ recipient_consignment: resumeSource, state: {
  transfer_id: id, batch_transfer_ids: resumeIDs, consignment_hash: await sha256Text(resumeSource), witness_txid: 'resume-witness',
  direction: 'send', transport_mode: 'out-of-band', status: 'prepared', asset: { Name: { Protocol: 'rgb11', Type: 'f', Ticker: 'test' } },
} })))
assert.equal(await rgb11ResumedPackageSetMatches(resumePackages, resumeIDs), true)
assert.equal(await rgb11ResumedPackageSetMatches(resumePackages.slice(0, 1), resumeIDs), false, 'missing batch recipient')
for (const field of ['transfer_id', 'witness_txid', 'consignment_hash', 'direction', 'status']) {
  const changed = structuredClone(resumePackages)
  changed[1].state[field] = 'different'
  assert.equal(await rgb11ResumedPackageSetMatches(changed, resumeIDs), false, `resume binding: ${field}`)
}
const changedSource = structuredClone(resumePackages)
changedSource[1].recipient_consignment = 'different-source'
assert.equal(await rgb11ResumedPackageSetMatches(changedSource, resumeIDs), false)
// SDK GetState ticker_infos[].name differs from transfer.asset.Name.
const sdkAssetName = { Protocol: 'rgb11', Type: 'f', Ticker: 'r1ui26@fingerprint' }
const sdkTickerState = { ticker_infos: [{ name: sdkAssetName, contract_id: 'rgb:original-contract', ticker: 'R1UI26' }] }
assert.deepEqual(rgb11TaskResumeAsset(sdkTickerState, { asset: { Name: sdkAssetName } }),
  { contract_id: 'rgb:original-contract', ticker: sdkAssetName.Ticker, protocol: 'rgb11', type: 'f' })
assert.equal(rgb11TaskResumeAsset(sdkTickerState, { asset: { Name: { ...sdkAssetName, Ticker: 'different-fingerprint' } } }), null)
assert.equal(rgb11TaskResumeAsset({ ticker_infos: [{ name: sdkAssetName }] }, { asset: { Name: sdkAssetName } }), null)
console.log('RGB11 OOB matching checks passed')
