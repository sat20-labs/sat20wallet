import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createHash } from 'node:crypto'
import { Psbt, Transaction, networks, payments, script, initEccLib } from 'bitcoinjs-lib'
import * as secp256k1 from '@bitcoin-js/tiny-secp256k1-asmjs'
import { expect } from '@playwright/test'

initEccLib(secp256k1)
export const requiredSDKWASMCases = [
  'SDK WASM: production namespaces match the complete export contract without debug secrets',
  'SDK WASM: missing arguments and wrong types return errors without terminating the runtime',
  'SDK WASM: mnemonic import and address validation preserve the independent fixture identity',
  'SDK WASM: wallet creation exports a valid phrase and password authentication survives reload',
  'SDK WASM: subaccount metadata and selection survive reload without changing root identity',
  'SDK WASM: data and Bitcoin message signatures independently verify the exact payload',
  'SDK WASM: PSBT and batch signatures preserve outputs and independently verify Taproot witnesses',
  'SDK WASM: UTXO owner locks persist and reject another origin before exact-owner unlock',
  'SDK WASM: operation log JSON roundtrip persists and deletion preserves wallet identity',
  'SDK WASM: invalid password mnemonic PSBT and root deletion preserve the catalog',
  'SDK WASM: negative fractional and overflowing account indexes are rejected without selection changes',
  'SDK WASM: release rejects stale calls and reinitialization recovers the same persisted wallet',
  'SDK WASM: unknown channel status returns from database fallback without blocking later calls',
  'SDK WASM: private-key wallets sign independently and switch rename delete across cold restart',
  'SDK WASM: managed mnemonic accounts reject private-key wallet mixing without catalog changes',
  'SDK WASM: monitor wallets read funded addresses and cannot sign or disclose a mnemonic',
  'SDK WASM: invalid monitor addresses preserve the existing selected wallet',
  'SDK WASM: L1 and L2 V2 selection and balances match independently indexed outputs',
  'SDK WASM: L2 owner locks reject another origin account and fingerprint across reload',
  'SDK WASM: asset-bearing L2 PSBT single batch extraction and signatures preserve exact outputs',
  'SDK WASM: L2 order split merge and input output helpers preserve signed asset metadata',
  'SDK WASM: invalid batch amounts PSBTs and missing channel safety targets never broadcast',
  'SDK WASM: unified contract content and query boundaries preserve funds and identity',
  'SDK WASM: issuance and referrer invalid parameters never sign or broadcast',
  'SDK WASM: invalid RGB invoices consignments and transport requests preserve proofs and locks',
  'SDK WASM: real L1 batch send confirms each requested recipient output and exact fee',
  'SDK WASM: real L2 batch send confirms each requested recipient output and exact fee',
  'SDK WASM: garbage send preserves selected value and uses only free plain fee inputs',
  'SDK WASM: all direct interface cases finish without unhandled browser errors',
]

const sha = bytes => createHash('sha256').update(bytes).digest()
const messageHash = payload => {
  const data = Buffer.from(payload)
  assert.ok(data.length < 253)
  return sha(sha(Buffer.concat([Buffer.from('\x18Bitcoin Signed Message:\n'), Buffer.from([data.length]), data])))
}

// Each case gets an independent IndexedDB and manager. Signing cases do not
// broadcast; the final send cases use the real node and controlled L1 ledger.
export async function runSDKWASMCases({ check, device, pageErrors }, fixture) {
  const actor = fixture.basicWallet
  const call = async (page, method, args = [], namespace = 'sat20wallet_wasm', failure = false) => {
    let timer
    let result
    try {
      result = await Promise.race([
        page.evaluate(async ({ namespace, method, args }) => {
          return await window[namespace][method](...args)
        }, { namespace, method, args }),
        new Promise((_, reject) => {
          timer = setTimeout(() => reject(new Error(`${method}: WASM request did not return within 30s`)), 30000)
        }),
      ])
    } finally {
      clearTimeout(timer)
    }
    assert.ok(result && typeof result === 'object', `${method}: response envelope missing`)
    assert.equal(typeof result.code, 'number', `${method}: code must be numeric`)
    assert.equal(typeof result.msg, 'string', `${method}: msg must be a string`)
    if (failure) assert.notEqual(result.code, 0, `${method}: invalid operation accepted`)
    else assert.equal(result.code, 0, `${method}: ${result.msg}`)
    return result.data
  }
  const imported = async page => {
    const result = await call(page, 'importWallet', [actor.mnemonic, actor.password])
    assert.equal(result.address, actor.address)
    return String(result.walletId)
  }
  const catalog = page => call(page, 'getWalletCatalog')
  const poll = { timeout: 180000, intervals: [250, 500, 1000, 2000] }
  const control = async (action, body = {}) => {
    const response = await fetch(new URL(`/${action}`, fixture.control_url), {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
      signal: AbortSignal.timeout(240000),
    })
    if (action === 'transaction' && response.status === 404) return null
    assert.equal(response.ok, true, `${action}: HTTP ${response.status}`)
    const result = await response.json()
    assert.ok(!result.error, result.error)
    return result
  }
  const api = async (layer, path) => {
    const endpoint = fixture.config[layer === 'L1' ? 'IndexerL1' : 'IndexerL2']
    const response = await fetch(`${endpoint.Scheme}://${endpoint.Host}/${String(endpoint.Proxy || 'testnet').replace(/^\/+|\/+$/g, '')}${path}`)
    assert.equal(response.ok, true, `${layer} ${path}: HTTP ${response.status}`)
    const result = await response.json()
    assert.equal(result.code, 0, result.msg)
    return result.data
  }
  const unchanged = async (page, before, l1) => {
    assert.deepEqual(await catalog(page), before)
    const after = await control('snapshot')
    assert.deepEqual(after.l1.broadcast_count, l1.broadcast_count)
    assert.deepEqual(after.l1.pending_txids, l1.pending_txids)
    assert.deepEqual(after.mempool_txids, [], 'invalid request must not submit an L2 transaction')
    assert.equal((await call(page, 'getWalletAddress', [0])).address, actor.address)
  }
  const tests = [
    async page => {
      const source = readFileSync(new URL('../../../sdk/wasm/production_exports_test.go', import.meta.url), 'utf8')
      const expected = source.match(/const productionWASMExportAllowlist = `([\s\S]*?)`/)[1].trim().split(/\s+/).sort()
      const namespaces = await page.evaluate(() => Object.fromEntries(
        ['sat20wallet_wasm', 'sat20account_wasm', 'sat20wallet_operation_log'].map(name =>
          [name, Object.keys(window[name]).filter(key => typeof window[name][key] === 'function')])))
      for (const names of Object.values(namespaces)) assert.ok(names.length > 0)
      const actual = Object.values(namespaces).flat().sort()
      assert.equal(new Set(actual).size, actual.length, 'unexpected duplicate exports across namespaces')
      assert.deepEqual(actual, expected)
      for (const name of ['dbTest', 'batchDbTest', 'getCommitRootKey', 'getCommitSecret', 'deriveRevocationPrivKey', 'getRevocationBaseKey']) {
        assert.ok(!actual.includes(name), `private/debug export present: ${name}`)
      }
      assert.equal(typeof (await call(page, 'getVersion')).version, 'string')
    },
    async page => {
      for (const method of ['importWallet', 'validateMnemonic', 'unlockWallet', 'changePassword', 'switchWallet', 'switchAccount',
        'ensureAccount', 'updateAccountMetadata', 'sendAssets', 'sendAssets_SatsNet', 'signData', 'signMessage',
        'signPsbt', 'signPsbts', 'lockToChannelWithExpand', 'lockUtxoForOwner']) await call(page, method, [], undefined, true)
      await call(page, 'importWallet', [123, false], undefined, true)
      await call(page, 'signPsbts', [['not-psbt'], 'true'], undefined, true)
      await call(page, 'beginOperationLog', [], 'sat20wallet_operation_log', true)
      for (const args of [[], [null], [123], ['not-a-function']]) {
        await call(page, 'registerCallback', args, undefined, true)
      }
      assert.equal(typeof (await call(page, 'getVersion')).version, 'string')
    },
    async page => {
      const derived = await call(page, 'validateMnemonic', [actor.mnemonic, ''])
      assert.equal(derived.address, actor.address)
      const normalized = await call(page, 'validateMnemonic', ['  ' + actor.mnemonic.replaceAll(' ', '  ') + '  ', ''])
      assert.equal(normalized.fingerprint, derived.fingerprint)
      const id = await imported(page)
      assert.equal((await call(page, 'getWalletAddress', [0])).address, actor.address)
      const publicKey = Buffer.from((await call(page, 'getWalletPubkey', [0])).pubKey, 'hex')
      assert.equal(payments.p2tr({ internalPubkey: publicKey.subarray(1), network: networks.testnet }).address, actor.address)
      assert.equal((await call(page, 'validateBitcoinAddress', [actor.address])).valid, true)
      assert.equal((await call(page, 'validateBitcoinAddress', ['not-an-address'])).valid, false)
      assert.equal((await call(page, 'getMnemonice', [id, actor.password])).mnemonic, actor.mnemonic)
    },
    async page => {
      const result = await call(page, 'createWallet', [actor.password])
      const validated = await call(page, 'validateMnemonic', [result.mnemonic, ''])
      assert.equal(validated.address, (await call(page, 'getWalletAddress', [0])).address)
      const before = await catalog(page)
      const changed = 'SDK-WASM-password-changed!'
      await call(page, 'changePassword', ['wrong-password', changed], undefined, true)
      await call(page, 'changePassword', [actor.password, changed])
      await page.reload(); await page.waitForFunction(() => Boolean(window.__SAT20_PWA_VERIFY__))
      await call(page, 'unlockWallet', [actor.password], undefined, true)
      await call(page, 'unlockWallet', [changed])
      assert.deepEqual((await catalog(page)).wallets, before.wallets)
      assert.equal((await call(page, 'getWalletAddress', [0])).address, validated.address)
    },
    async page => {
      const id = await imported(page)
      const root = (await catalog(page)).root_account_id
      await call(page, 'ensureAccount', [id, 1, 'SDK child'])
      await call(page, 'updateAccountMetadata', [id, 1, 'SDK renamed'])
      await call(page, 'switchAccount', [1])
      assert.equal((await catalog(page)).current_account_index, 1)
      const address = (await call(page, 'getWalletAddress', [1])).address
      assert.notEqual(address, actor.address)
      await page.reload(); await page.waitForFunction(() => Boolean(window.__SAT20_PWA_VERIFY__))
      await call(page, 'unlockWallet', [actor.password])
      assert.equal((await catalog(page)).root_account_id, root)
      assert.equal((await catalog(page)).current_account_index, 1)
      assert.equal((await call(page, 'getWalletAddress', [1])).address, address)
      await call(page, 'switchAccount', [0])
      assert.equal((await call(page, 'getWalletAddress', [0])).address, actor.address)
    },
    async page => {
      await imported(page)
      const publicKey = Buffer.from((await call(page, 'getWalletPubkey', [0])).pubKey, 'hex')
      const payload = 'SDK WASM independent signature 2026'
      const der = Buffer.from((await call(page, 'signData', [payload])).signature, 'hex')
      const signature = script.signature.decode(Buffer.concat([der, Buffer.from([1])])).signature
      assert.equal(secp256k1.verify(sha(payload), publicKey, signature), true)
      assert.equal(secp256k1.verify(sha(payload + '!'), publicKey, signature), false)
      const compact = Buffer.from((await call(page, 'signMessage', [payload])).signature, 'base64')
      assert.equal(compact.length, 65); assert.ok(compact[0] >= 31 && compact[0] <= 34)
      assert.equal(secp256k1.verify(messageHash(payload), publicKey, compact.subarray(1)), true)
      assert.equal(secp256k1.verify(messageHash(payload + '!'), publicKey, compact.subarray(1)), false)
    },
    async page => {
      await imported(page)
      const endpoint = fixture.config.IndexerL1
      const api = async path => {
        const response = await fetch(`${endpoint.Scheme}://${endpoint.Host}/${endpoint.Proxy}${path}`)
        assert.equal(response.status, 200)
        const result = await response.json(); assert.equal(result.code, 0); return result.data
      }
      const input = (await api(`/v3/address/utxos/${actor.address}`)).find(item => !(item.Assets || []).length && item.Value >= 10000)
      assert.ok(input)
      const [hash, index] = input.Outpoint.split(':')
      const previous = Transaction.fromHex(await api(`/btc/rawtx/${hash}`)).outs[Number(index)]
      const publicKey = Buffer.from((await call(page, 'getWalletPubkey', [0])).pubKey, 'hex')
      const psbt = new Psbt({ network: networks.testnet })
      psbt.addInput({ hash, index: Number(index), sequence: 0xfffffffd,
        witnessUtxo: previous, tapInternalKey: publicKey.subarray(1) })
      psbt.addOutput({ address: fixture.recipient.address, value: 2000 })
      psbt.addOutput({ address: actor.address, value: previous.value - 2500 })
      const encoded = psbt.toHex()
      const single = (await call(page, 'signPsbt', [encoded, false])).psbt
      const batch = (await call(page, 'signPsbts', [[encoded, encoded], false])).psbts
      assert.equal(batch.length, 2)
      for (const signed of [single, ...batch]) {
        const parsed = Psbt.fromHex(signed, { network: networks.testnet })
        assert.deepEqual(parsed.data.globalMap.unsignedTx.toBuffer(), psbt.data.globalMap.unsignedTx.toBuffer())
        const tx = parsed.extractTransaction(); const sig = tx.ins[0].witness[0]
        assert.ok(sig.length === 64 || sig.length === 65)
        const sighash = sig.length === 65 ? sig[64] : Transaction.SIGHASH_DEFAULT
        assert.equal(secp256k1.verifySchnorr(tx.hashForWitnessV1(0, [previous.script], [previous.value], sighash), previous.script.subarray(2), sig.subarray(0, 64)), true)
        assert.equal((await call(page, 'extractTxFromPsbt', [signed])).tx, tx.toHex())
      }
    },
    async page => {
      await imported(page)
      const utxos = (await call(page, 'getUtxosWithAsset', [actor.address, '1000', '::'])).utxos
      assert.ok(utxos.length); const outpoint = utxos[0]
      assert.match(outpoint, /^[0-9a-f]{64}:\d+$/)
      const current = (await catalog(page)).wallets[0]
      const owner = { origin: 'http://127.0.0.1:9001', network: 'testnet', wallet_fingerprint: current.fingerprint, account_index: 0 }
      await call(page, 'lockUtxoForOwner', [actor.address, outpoint, 'SDK WASM E2E', JSON.stringify(owner)])
      assert.equal(await call(page, 'isUtxoLocked', [actor.address, outpoint]), true)
      await page.reload(); await page.waitForFunction(() => Boolean(window.__SAT20_PWA_VERIFY__))
      await call(page, 'unlockWallet', [actor.password])
      assert.equal(await call(page, 'isUtxoLocked', [actor.address, outpoint]), true)
      await call(page, 'unlockUtxoForOwner', [actor.address, outpoint, JSON.stringify({ ...owner, origin: 'http://127.0.0.1:9002' })], undefined, true)
      assert.equal(await call(page, 'isUtxoLocked', [actor.address, outpoint]), true)
      await call(page, 'unlockUtxoForOwner', [actor.address, outpoint, JSON.stringify(owner)])
      assert.equal(await call(page, 'isUtxoLocked', [actor.address, outpoint]), false)
    },
    async page => {
      await imported(page)
      const namespace = 'sat20wallet_operation_log'
      const { id } = await call(page, 'beginOperationLog', [JSON.stringify({ category: 'signature', action: 'sdk-e2e', title: 'SDK log', summary: 'roundtrip', parameters: { payload: 'public test data' } })], namespace)
      await call(page, 'updateOperationLog', [id, JSON.stringify({ status: 'succeeded', message: 'independently verified', result: { verified: 'true' } })], namespace)
      const before = JSON.parse((await call(page, 'getOperationLog', [id], namespace)).log)
      await page.reload(); await page.waitForFunction(() => Boolean(window.__SAT20_PWA_VERIFY__))
      await call(page, 'unlockWallet', [actor.password])
      assert.deepEqual(JSON.parse((await call(page, 'getOperationLog', [id], namespace)).log), before)
      assert.ok(JSON.parse((await call(page, 'getOperationLogs', [], namespace)).logs).some(log => log.id === id))
      await call(page, 'deleteAllOperationLogs', [], namespace)
      assert.equal(JSON.parse((await call(page, 'getOperationLogs', [], namespace)).logs).length, 0)
      assert.equal((await call(page, 'getWalletAddress', [0])).address, actor.address)
    },
    async page => {
      const id = await imported(page); const before = await catalog(page)
      // This legacy export signals authentication failure with an empty phrase.
      // Assert no secret is disclosed, rather than imposing a new error ABI.
      assert.equal((await call(page, 'getMnemonice', [id, 'wrong'])).mnemonic, '')
      for (const [method, args] of [['changePassword', ['wrong', 'new-password']],
        ['importWallet', ['invalid mnemonic', actor.password]], ['signPsbt', ['70736274ff00', false]],
        ['signPsbts', [['70736274ff00'], false]], ['deleteWallet', [id]]]) await call(page, method, args, undefined, true)
      assert.deepEqual(await catalog(page), before)
      assert.equal((await call(page, 'getWalletAddress', [0])).address, actor.address)
    },
    async page => {
      const id = await imported(page); await call(page, 'ensureAccount', [id, 1, 'boundary child'])
      for (const index of [-1, 1.25, 2 ** 32, Number.MAX_SAFE_INTEGER, NaN, Infinity, -Infinity]) {
        console.log(JSON.stringify({ observation: 'account-index boundary', index: String(index) }))
        const before = await catalog(page)
        for (const method of ['switchAccount', 'getWalletAddress', 'getWalletPubkey']) {
          await call(page, method, [index], undefined, true)
          assert.deepEqual(await catalog(page), before, `${method} changed the catalog for ${String(index)}`)
        }
        await call(page, 'ensureAccount', [id, index, 'invalid'], undefined, true)
        assert.deepEqual(await catalog(page), before)
      }
      for (const index of [0, 1]) {
        await call(page, 'switchAccount', [index])
        assert.equal((await catalog(page)).current_account_index, index)
        const address = (await call(page, 'getWalletAddress', [index])).address
        const publicKey = Buffer.from((await call(page, 'getWalletPubkey', [index])).pubKey, 'hex')
        assert.equal(payments.p2tr({ internalPubkey: publicKey.subarray(1), network: networks.testnet }).address, address)
        if (index === 0) assert.equal(address, actor.address)
        else assert.notEqual(address, actor.address)
      }
    },
    async page => {
      await imported(page); const before = await catalog(page)
      await call(page, 'init', [fixture.config, 2], undefined, true)
      assert.deepEqual(await catalog(page), before)
      await call(page, 'release')
      await call(page, 'signData', ['after-release'], undefined, true)
      await call(page, 'getWalletCatalog', [], undefined, true)
      await call(page, 'release', [], undefined, true)
      await call(page, 'init', [{ ...fixture.config, Peers: 'not-an-array' }, 2], undefined, true)
      await call(page, 'getWalletCatalog', [], undefined, true)
      await call(page, 'init', [fixture.config, 2])
      await call(page, 'unlockWallet', [actor.password])
      assert.deepEqual(await catalog(page), before)
      assert.equal((await call(page, 'getWalletAddress', [0])).address, actor.address)
    },
    async page => {
      await imported(page)
      const before = await catalog(page)
      await call(page, 'getChannelStatus', [], undefined, true)
      await call(page, 'getChannelStatus', [123], undefined, true)
      const publicKey = Buffer.from((await call(page, 'getWalletPubkey', [0])).pubKey, 'hex')
      // This fresh wallet has no channels. Use a valid 2-of-2 P2WSH address
      // to exercise FindChannel's IndexedDB miss rather than a bad argument.
      const unknownChannel = payments.p2wsh({ network: networks.testnet,
        redeem: { output: script.compile([0x52, publicKey, publicKey, 0x52, 0xae]) } }).address
      assert.ok(unknownChannel)
      let timeout
      try {
        const status = await Promise.race([call(page, 'getChannelStatus', [unknownChannel]),
          new Promise((_, reject) => {
            timeout = setTimeout(() => reject(new Error('getChannelStatus database fallback timed out after 30s')), 30000)
          })])
        assert.equal(status, 0, 'unknown channel must return CS_UNKNOWN')
      } finally {
        clearTimeout(timeout)
      }
      assert.equal(typeof (await call(page, 'getVersion')).version, 'string')
      assert.equal((await call(page, 'getWalletAddress', [0])).address, actor.address)
      assert.deepEqual(await catalog(page), before)
    },
    async page => {
      const password = 'SDK-private-key-local!'
      const keys = [1, 2].map(value => Buffer.from(value.toString(16).padStart(64, '0'), 'hex'))
      const addresses = keys.map(key => payments.p2tr({ internalPubkey: Buffer.from(secp256k1.pointFromScalar(key, true)).subarray(1), network: networks.testnet }).address)
      const ids = []
      for (const [index, key] of keys.entries()) {
        const imported = await call(page, 'importWalletWithPrivKey', [key.toString('hex'), password])
        assert.equal(imported.address, addresses[index]); ids.push(imported.walletId)
        const payload = `private-key signature ${index}`
        const der = Buffer.from((await call(page, 'signData', [payload])).signature, 'hex')
        const sig = script.signature.decode(Buffer.concat([der, Buffer.from([1])])).signature
        assert.equal(secp256k1.verify(sha(payload), Buffer.from(secp256k1.pointFromScalar(key, true)), sig), true)
      }
      await call(page, 'updateWalletName', [ids[0], 'SDK standalone key'])
      await call(page, 'switchWallet', [ids[0], password])
      assert.equal((await call(page, 'getWalletAddress', [0])).address, addresses[0])
      const unlockedCatalog = await catalog(page)
      await page.reload(); await page.waitForFunction(() => Boolean(window.__SAT20_PWA_VERIFY__))
      await call(page, 'unlockWallet', ['wrong'], undefined, true)
      await call(page, 'getWalletAddress', [0], undefined, true)
      await call(page, 'unlockWallet', [password])
      assert.deepEqual(await catalog(page), unlockedCatalog)
      assert.equal((await call(page, 'getWalletAddress', [0])).address, addresses[0])
      assert.ok((await catalog(page)).wallets.some(wallet => String(wallet.id) === ids[0] && wallet.name === 'SDK standalone key'))
      await call(page, 'deleteWallet', [ids[1]])
      assert.ok(!(await catalog(page)).wallets.some(wallet => String(wallet.id) === ids[1]))
      await call(page, 'switchWallet', [ids[1], password], undefined, true)
      assert.equal((await call(page, 'getWalletAddress', [0])).address, addresses[0])
    },
    async page => {
      const discovery = await call(page, 'recoverAccountManagementFromRootMnemonic', [actor.mnemonic, actor.password])
      assert.equal(discovery.status, 'not_found', 'managed import requires authoritative root discovery')
      await imported(page); const before = await catalog(page)
      assert.equal((await call(page, 'status', [], 'sat20account_wasm')).active, true)
      for (const key of ['not-hex', '0'.repeat(64), '1'.padStart(64, '0')]) {
        await call(page, 'importWalletWithPrivKey', [key, actor.password], undefined, true)
        assert.deepEqual(await catalog(page), before)
      }
      assert.equal((await call(page, 'getWalletAddress', [0])).address, actor.address)
    },
    async page => {
      // The current monitor implementation is explicitly nonpersistent. This
      // checks its supported read-only scope; persistence remains a product gap.
      const id = (await call(page, 'createMonitorWallet', [actor.address])).walletId
      assert.equal((await call(page, 'getWalletAddress', [0])).address, actor.address)
      const expected = await api('L1', `/v3/address/summary/${actor.address}`)
      assert.ok(expected.length > 0)
      assert.ok(BigInt((await call(page, 'getAssetAmount', [actor.address, '::'])).availableAmt) > 0n)
      for (const method of ['signData', 'signMessage']) await call(page, method, ['monitor cannot sign'], undefined, true)
      assert.equal((await call(page, 'getMnemonice', [id, actor.password])).mnemonic, '')
      assert.equal(typeof (await call(page, 'getVersion')).version, 'string')
    },
    async page => {
      await imported(page); const before = await catalog(page)
      const snapshot = await control('snapshot')
      for (const address of ['', 'not-an-address', payments.p2tr({ internalPubkey: Buffer.from(secp256k1.pointFromScalar(Buffer.from('1'.padStart(64, '0'), 'hex'), true)).subarray(1), network: networks.bitcoin }).address]) {
        await call(page, 'createMonitorWallet', [address], undefined, true)
        await unchanged(page, before, snapshot.l1)
      }
    },
    async page => {
      await imported(page)
      for (const [layer, suffix] of [['L1', ''], ['L2', '_SatsNet']]) {
        const outputs = await api(layer, `/v3/address/asset/${actor.address}/::`)
        assert.ok(outputs.length > 0)
        const points = new Set(outputs.map(output => output.Outpoint))
        const selected = await call(page, `getUtxosWithAssetV2${suffix}`, [actor.address, 1000, '1000', '::'])
        assert.ok(selected.utxos.length > 0)
        assert.equal(new Set(selected.utxos).size, selected.utxos.length)
        for (const point of [...selected.utxos, ...selected.fees]) assert.ok(points.has(point), `${layer}: selected unindexed output ${point}`)
        const total = outputs.reduce((sum, output) => sum + BigInt(output.Value), 0n)
        const balance = await call(page, `getAssetAmount${suffix}`, [actor.address, '::'])
        assert.equal(BigInt(balance.availableAmt) + BigInt(balance.lockedAmt), total)
        const excessive = (total + 1n).toString()
        await call(page, `getUtxosWithAsset${suffix}`, [actor.address, excessive, '::'], undefined, true)
      }
      assert.equal((await call(page, 'validateSatsNetAddress', [actor.address])).valid, true)
      assert.equal((await call(page, 'validateSatsNetAddress', ['invalid'])).valid, false)
    },
    async page => {
      await imported(page)
      const outpoint = (await call(page, 'getUtxosWithAsset_SatsNet', [actor.address, '1000', '::'])).utxos[0]
      assert.ok(outpoint)
      const owner = { origin: 'http://127.0.0.1:9001', network: 'testnet', wallet_fingerprint: (await catalog(page)).wallets[0].fingerprint, account_index: 0 }
      await call(page, 'lockUtxoForOwner_SatsNet', [actor.address, outpoint, 'SDK L2 owner', JSON.stringify(owner)])
      await page.reload(); await page.waitForFunction(() => Boolean(window.__SAT20_PWA_VERIFY__))
      await call(page, 'unlockWallet', [actor.password])
      for (const wrong of [{ ...owner, origin: 'http://127.0.0.1:9002' }, { ...owner, account_index: 1 }, { ...owner, wallet_fingerprint: 'another-wallet' }]) {
        await call(page, 'unlockUtxoForOwner_SatsNet', [actor.address, outpoint, JSON.stringify(wrong)], undefined, true)
        assert.equal(await call(page, 'isUtxoLocked_SatsNet', [actor.address, outpoint]), true)
      }
      await call(page, 'unlockUtxoForOwner_SatsNet', [actor.address, outpoint, JSON.stringify(owner)])
      assert.equal(await call(page, 'isUtxoLocked_SatsNet', [actor.address, outpoint]), false)
    },
    async page => {
      await imported(page)
      const sample = fixture.sdkPSBT
      assert.ok(sample?.psbt && sample.unsignedTx, 'asset-bearing Go PSBT fixture is required')
      assert.equal((await call(page, 'extractUnsignedTxFromPsbt_SatsNet', [sample.psbt])).tx, sample.unsignedTx)
      await call(page, 'extractTxFromPsbt_SatsNet', [sample.psbt], undefined, true)
      const single = (await call(page, 'signPsbt_SatsNet', [sample.psbt, false])).psbt
      const batch = (await call(page, 'signPsbts_SatsNet', [[sample.psbt, sample.psbt], false])).psbts
      assert.equal(batch.length, 2)
      for (const signed of [single, ...batch]) {
        const observed = await control('inspect-psbt', { psbt: signed, verify: true })
        assert.equal(observed.unsigned_tx, sample.unsignedTx)
        assert.deepEqual(observed.inputs, sample.inputs)
        assert.deepEqual(observed.outputs, sample.outputs)
        assert.equal(observed.signatures_verified, true)
        assert.equal((await call(page, 'extractTxFromPsbt_SatsNet', [signed])).tx, observed.tx)
        const info = await call(page, 'getTxAssetInfoFromPsbt_SatsNet', [signed])
        assert.equal(info.txHex, sample.unsignedTx)
        assert.equal(info.txId, observed.txid)
      }
      const extracted = (await call(page, 'signPsbt_SatsNet', [sample.psbt, true])).psbt
      const observed = await control('inspect-psbt', { psbt: single, verify: true })
      assert.equal(extracted, observed.tx)
      for (const method of ['signPsbt_SatsNet', 'extractTxFromPsbt_SatsNet', 'extractUnsignedTxFromPsbt_SatsNet', 'getTxAssetInfoFromPsbt_SatsNet']) {
        await call(page, method, ['70736274ff00', false], undefined, true)
      }
      for (const tampered of sample.tampered) {
        await call(page, 'signPsbt_SatsNet', [tampered, false], undefined, true)
      }
    },
    async page => {
      await imported(page)
      const sample = fixture.sdkPSBT
      const built = (await call(page, 'buildBatchSellOrder_SatsNet', [sample.orders, actor.address, 'testnet'])).psbt
      const signed = (await call(page, 'signPsbt_SatsNet', [built, false])).psbt
      const before = await control('inspect-psbt', { psbt: signed, verify: true })
      assert.equal(before.inputs.length, 2)
      assert.deepEqual(before.inputs.map(input => input.outpoint), sample.orderOutpoints)
      assert.deepEqual(before.outputs.map(output => output.value), [1000, 2000])
      const split = (await call(page, 'splitBatchSignedPsbt_SatsNet', [signed, 'testnet'])).psbts
      assert.equal(split.length, 2)
      for (const [index, piece] of split.entries()) {
        const actual = await control('inspect-psbt', { psbt: piece, verify: true })
        assert.deepEqual(actual.inputs, [before.inputs[index]])
        assert.deepEqual(actual.outputs, [before.outputs[index]])
      }
      const merged = (await call(page, 'mergeBatchSignedPsbt_SatsNet', [split, 'testnet'])).psbt
      assert.deepEqual(await control('inspect-psbt', { psbt: merged, verify: true }), before)
      const withInput = (await call(page, 'addInputsToPsbt_SatsNet', [sample.psbt, [sample.additionalInput]])).psbt
      const withOutput = (await call(page, 'addOutputsToPsbt_SatsNet', [withInput, [sample.additionalOutput]])).psbt
      const appended = await control('inspect-psbt', { psbt: withOutput })
      assert.deepEqual(appended.inputs.slice(0, 1), sample.inputs)
      assert.deepEqual(appended.outputs.slice(0, 2), sample.outputs)
      assert.equal(appended.inputs.at(-1).outpoint, sample.orderOutpoints[1])
      assert.equal(appended.outputs.at(-1).value, sample.additionalValue)
      const finalized = (await call(page, 'finalizeSellOrder_SatsNet', [signed, [sample.buyerOrder], fixture.recipient.address, actor.address, 'testnet', 100, 500])).psbt
      const buyer = await device()
      try {
        await call(buyer, 'importWallet', [fixture.recipient.mnemonic, fixture.recipient.password])
        const complete = (await call(buyer, 'signPsbt_SatsNet', [finalized, false])).psbt
        const order = await control('inspect-psbt', { psbt: complete, verify: true })
        assert.deepEqual(order.inputs.slice(0, 2), before.inputs)
        assert.deepEqual(order.outputs.slice(0, 2), before.outputs)
        assert.equal(order.inputs.at(-1).outpoint, JSON.parse(sample.buyerOrder).Outpoint)
        assert.equal(order.outputs[2].pk_script, fixture.recipient.pk_script)
        assert.equal(order.outputs[2].value, 1_000_000 + sample.buyerValue - 3000 - 100 - 500)
        assert.equal(order.outputs[3].pk_script, actor.pk_script)
        assert.equal(order.outputs[3].value, 100)
        assert.equal(order.outputs[2].assets[0].Amount.Value, '50000000')
        assert.equal(order.signatures_verified, true)
      } finally { await buyer.context().close() }
      await call(page, 'mergeBatchSignedPsbt_SatsNet', [[split[0], split[0]], 'testnet'], undefined, true)
      await call(page, 'addInputsToPsbt_SatsNet', [sample.psbt, [sample.orders[0]]], undefined, true)
      for (const method of ['buildBatchSellOrder_SatsNet', 'mergeBatchSignedPsbt_SatsNet']) await call(page, method, [['invalid'], actor.address, 'testnet'], undefined, true)
      await call(page, 'finalizeSellOrder_SatsNet', ['invalid', [], fixture.recipient.address, actor.address, 'testnet', 100, 100], undefined, true)
    },
    async page => {
      await imported(page); const before = await catalog(page)
      const l1 = (await control('snapshot')).l1
      for (const amount of ['0', '-1', '0.1', '18446744073709551616']) {
        for (const suffix of ['', '_SatsNet']) {
          const args = [fixture.recipient.address, '::', amount, 2, '1']
          await call(page, `batchSendAssets${suffix}`, args, undefined, true)
          await unchanged(page, before, l1)
        }
        await call(page, 'batchSendAssetsV2_SatsNet', [[fixture.recipient.address], '::', [amount]], undefined, true)
        await unchanged(page, before, l1)
      }
      for (const count of [0, -1, 1.25, 2 ** 31, NaN, Infinity, -Infinity]) {
        for (const suffix of ['', '_SatsNet']) {
          await call(page, `batchSendAssets${suffix}`, [fixture.recipient.address, '::', '1000', count, '1'], undefined, true)
          await unchanged(page, before, l1)
        }
      }
      await call(page, 'batchSendAssetsV2_SatsNet', [[fixture.recipient.address], '::', ['1000', '2000']], undefined, true)
      await unchanged(page, before, l1)
      for (const method of ['safetySnapshot', 'commitmentExport', 'forceClosePlan', 'sweepBuild', 'punishBuild', 'reopenChannel', 'rebuildChannel']) {
        await call(page, method, ['missing-channel', '0'.repeat(64), 0, false], undefined, true)
        await unchanged(page, before, l1)
      }
      const punish = await call(page, 'punishStatus', ['missing-channel'])
      assert.equal(punish.channel_id, 'missing-channel')
      assert.deepEqual(JSON.parse(punish.json), [])
      await unchanged(page, before, l1)
    },
    async page => {
      await imported(page); const before = await catalog(page)
      const l1 = (await control('snapshot')).l1
      const content = { assetAName: fixture.tools.gasAsset, assetBName: '::', priceMode: 'height', steps: [{ threshold: '0', bPerA: '0.001' }] }
      const encoded = await call(page, 'buildUnifiedContractContent', ['template', 'exchange.tc', JSON.stringify(content)])
      assert.equal(encoded.contentEncoding, 'base64')
      assert.equal(encoded.content, fixture.sdkPSBT.exchangeContent, 'template encoder must match the independent Go protocol encoder')
      for (const method of ['estimateDeployUnifiedContract', 'deployUnifiedContract', 'invokeUnifiedContract', 'getFeeForInvokeUnifiedContract', 'queryContract']) {
        await call(page, method, ['{invalid'], undefined, true)
        await unchanged(page, before, l1)
      }
      await call(page, 'buildUnifiedContractContent', ['unknown-module', 'unknown-template', '{}'], undefined, true)
      await call(page, 'getParamForInvokeUnifiedContract', ['template', 'unknown-template', 'unknown-action'], undefined, true)
      await call(page, 'queryContract', [JSON.stringify({ Query: 'state', Contract: 'invalid' })], undefined, true)
      await unchanged(page, before, l1)
    },
    async page => {
      await imported(page); const before = await catalog(page)
      const l1 = (await control('snapshot')).l1
      for (const [method, args] of [
        ['deployTickerOrdx', ['SDKBAD', '-1', '0', 1, '1']],
        ['deployTickerBrc20', ['BAD', '0', '-1', '19', '1']],
        ['mintAssetOrdx', ['PWAMINT', '0.1', '1']],
        ['mintAssetBrc20', ['missing', '-1', '1']], ['mintAssetRunes', ['missing', '1']],
        ['inscribeName', ['invalid/name', '1']], ['bindReferrerForServer', ['missing-referrer', '1']],
      ]) {
        await call(page, method, args, undefined, true)
        await unchanged(page, before, l1)
      }
    },
    async page => {
      await imported(page); const before = await catalog(page)
      const state = await call(page, 'getRGB11State')
      const locks = await call(page, 'getAllLockedUtxo', [actor.address])
      const l1 = (await control('snapshot')).l1
      for (const [method, args] of [
        ['acceptRGB11Consignment', ['missing-request', 'invalid-consignment']],
        ['importRGB11Contract', ['invalid-contract']],
        ['prepareRGB11Transfer', [JSON.stringify({ invoice: 'invalid', amount: '-1' })]],
        ['prepareRGB11AddressTransfer', [JSON.stringify({ address: 'invalid', amount: '0' })]],
        ['resolveRGB11AddressEndpoint', ['invalid']],
        ['receiveRGB11ProxyConsignment', [JSON.stringify({ consignment: 'invalid' })]],
        ['resumeRGB11PreparedTransfer', ['missing-request']],
      ]) {
        await call(page, method, args, undefined, true)
        assert.deepEqual(await call(page, 'getRGB11State'), state)
        assert.deepEqual(await call(page, 'getAllLockedUtxo', [actor.address]), locks)
        await unchanged(page, before, l1)
      }
    },
    async page => {
      await imported(page)
      const before = BigInt((await call(page, 'getAssetAmount', [fixture.recipient.address, '::'])).availableAmt)
      const { txId, fee: reportedFee } = await call(page, 'batchSendAssets', [fixture.recipient.address, '::', '1000', 2, '1'])
      const tx = Transaction.fromHex(await api('L1', `/btc/rawtx/${txId}`))
      assert.equal(tx.getId(), txId)
      const destination = payments.p2tr({ address: fixture.recipient.address, network: networks.testnet }).output
      assert.deepEqual(tx.outs.filter(output => output.script.equals(destination)).map(output => output.value), [1000, 1000])
      let inputValue = 0
      for (const input of tx.ins) {
        const previous = Transaction.fromHex(await api('L1', `/btc/rawtx/${Buffer.from(input.hash).reverse().toString('hex')}`)).outs[input.index]
        inputValue += previous.value; assert.ok(input.witness.length > 0)
      }
      const fee = inputValue - tx.outs.reduce((sum, output) => sum + output.value, 0)
      assert.equal(fee, reportedFee, 'reported fee must equal independently decoded input minus output value')
      assert.ok(fee >= tx.virtualSize(), 'batch fee must fund its actual signed vsize at 1 sat/vB')
      await control('confirm-l1', { wait_anchors: false })
      await expect.poll(async () => BigInt((await call(page, 'getAssetAmount', [fixture.recipient.address, '::'])).availableAmt), poll).toBe(before + 2000n)
    },
    async page => {
      await imported(page)
      const before = BigInt((await call(page, 'getAssetAmount_SatsNet', [fixture.recipient.address, '::'])).availableAmt)
      const { txId } = await call(page, 'batchSendAssetsV2_SatsNet', [[fixture.recipient.address, fixture.recipient.address], '::', ['1000', '2000']])
      await control('mine')
      await expect.poll(async () => (await control('transaction', { txid: txId }))?.confirmations ?? 0, poll).toBeGreaterThan(0)
      const tx = await control('transaction', { txid: txId })
      assert.equal(tx.txid, txId)
      assert.deepEqual(tx.vout.flatMap((output, index) => output.scriptPubKey.hex === fixture.recipient.pk_script ? [tx.output_sats[index]] : []), [1000, 2000])
      assert.ok(tx.vin.every(input => input.txinwitness?.length > 0))
      let inputValue = 0
      for (const input of tx.vin) {
        const previous = await control('transaction', { txid: input.txid })
        assert.ok(previous, 'each batch input must exist independently on the node')
        inputValue += previous.output_sats[input.vout]
      }
      assert.equal(inputValue - tx.output_sats.reduce((sum, value) => sum + value, 0), fixture.sdkPSBT.l2BatchFee)
      await expect.poll(async () => BigInt((await call(page, 'getAssetAmount_SatsNet', [fixture.recipient.address, '::'])).availableAmt), poll).toBe(before + 3000n)
    },
    async page => {
      await imported(page)
      const outputs = await api('L1', `/v3/address/utxos/${actor.address}`)
      const selected = outputs.find(output => !output.Assets?.length && output.Value > 10000)
      assert.ok(selected)
      const before = (await control('snapshot')).l1
      await call(page, 'lockUtxo', [actor.address, selected.Outpoint, 'garbage owner guard'])
      await call(page, 'sendGarbage', [fixture.recipient.address, [selected.Outpoint], 1000, '1'], undefined, true)
      assert.deepEqual((await control('snapshot')).l1.broadcast_count, before.broadcast_count)
      assert.equal(await call(page, 'isUtxoLocked', [actor.address, selected.Outpoint]), true)
      await call(page, 'unlockUtxo', [actor.address, selected.Outpoint])
      const freePlain = new Set((await api('L1', `/v3/address/asset/${actor.address}/::`)).map(output => output.Outpoint))
      const locks = await call(page, 'getAllLockedUtxo', [actor.address])
      const recipientBefore = BigInt((await call(page, 'getAssetAmount', [fixture.recipient.address, '::'])).availableAmt)
      const walletBefore = BigInt((await call(page, 'getAssetAmount', [actor.address, '::'])).availableAmt)
      const { txId } = await call(page, 'sendGarbage', [fixture.recipient.address, [selected.Outpoint], 1000, '1'])
      const tx = Transaction.fromHex(await api('L1', `/btc/rawtx/${txId}`))
      assert.equal(tx.getId(), txId)
      const points = tx.ins.map(input => `${Buffer.from(input.hash).reverse().toString('hex')}:${input.index}`)
      assert.equal(new Set(points).size, points.length)
      assert.equal(points.filter(point => point === selected.Outpoint).length, 1)
      let inputValue = 0
      for (const [index, point] of points.entries()) {
        const indexed = outputs.find(output => output.Outpoint === point)
        assert.ok(indexed, `garbage input ${point} must belong to the independently indexed wallet`)
        assert.ok(!Object.hasOwn(locks, point), `garbage fee input ${point} must be unlocked`)
        if (point !== selected.Outpoint) {
          assert.ok(freePlain.has(point), `garbage fee input ${point} must be plain`)
          assert.ok(!indexed.Assets?.length, `garbage fee input ${point} must not carry an asset`)
        }
        const input = tx.ins[index]
        const previous = Transaction.fromHex(await api('L1', `/btc/rawtx/${Buffer.from(input.hash).reverse().toString('hex')}`)).outs[input.index]
        assert.equal(previous.value, indexed.Value)
        inputValue += previous.value
      }
      assert.ok(tx.ins.every(input => input.witness.length > 0))
      const script = payments.p2tr({ address: fixture.recipient.address, network: networks.testnet }).output
      // Explicit garbage inputs transfer their full selected value. Additional
      // plain inputs pay the fee and return change to this wallet.
      assert.deepEqual(tx.outs.filter(output => output.script.equals(script)).map(output => output.value), [selected.Value])
      const fee = inputValue - tx.outs.reduce((sum, output) => sum + output.value, 0)
      assert.ok(fee >= tx.virtualSize())
      await control('confirm-l1', { wait_anchors: false })
      await expect.poll(async () => BigInt((await call(page, 'getAssetAmount', [fixture.recipient.address, '::'])).availableAmt), poll).toBe(recipientBefore + BigInt(selected.Value))
      await expect.poll(async () => BigInt((await call(page, 'getAssetAmount', [actor.address, '::'])).availableAmt), poll).toBe(walletBefore - BigInt(selected.Value) - BigInt(fee))
    },
  ]
  assert.equal(tests.length + 1, requiredSDKWASMCases.length, 'every registered SDK case needs an executable body')
  for (const [index, test] of tests.entries()) {
    let page
    try {
      await check(requiredSDKWASMCases[index], async () => { page = await device(); await test(page) })
    } catch (error) {
      console.error(JSON.stringify({ suite: 'SDK WASM', case: requiredSDKWASMCases[index], message: error.message }))
      process.exitCode = 1
    } finally { await page?.context().close() }
  }
  await check(requiredSDKWASMCases.at(-1), async () => assert.deepEqual(pageErrors, []))
}
