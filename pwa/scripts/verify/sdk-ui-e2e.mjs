import assert from 'node:assert/strict'
import { readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { Transaction } from 'bitcoinjs-lib'
import { expect } from '@playwright/test'

export const sdkUICases = [
  'SDK UI: RGB manual transfer inflation cancellation and reload',
  'SDK UI: Direct offline receive ignores another spent output',
  'SDK UI: paid two-factor recovery restore and renewal',
  'SDK UI: two-factor backup cancel reload and cold recovery',
  'SDK UI: Guardian entry points are disabled',
  'SDK UI: RGB blind receive and UDA lifecycle',
  'SDK UI: RGB proxy witness blind and resume',
  'SDK UI: DID availability submission and history',
  'SDK UI: wallet reimport while deletion pending',
  'SDK UI: wallet reimport after deletion confirmed',
  'SDK UI: partial recovery stays incomplete',
]
export const sdkAccountCases = [...sdkUICases.slice(2, 5), ...sdkUICases.slice(8)]
export const sdkUIRoles = {
  [sdkUICases[0]]: ['manual_owner', 'manual_receiver'],
  [sdkUICases[1]]: ['direct_owner', 'direct_receiver'],
  [sdkUICases[2]]: ['account_owner'],
  [sdkUICases[3]]: ['recovery_owner'],
  [sdkUICases[4]]: ['guardian_owner'],
  [sdkUICases[5]]: ['collectible_owner', 'collectible_receiver'],
  [sdkUICases[6]]: ['proxy_owner', 'proxy_receiver'],
  [sdkUICases[7]]: ['did_owner'],
  [sdkUICases[8]]: ['pending_owner', 'pending_leaf'],
  [sdkUICases[9]]: ['deleted_owner', 'deleted_leaf'],
  [sdkUICases[10]]: ['partial_owner'],
}

// Tests call the actual production WASM surface. Only the fixture controller
// mines real blocks; no balance, proof, mailbox or indexer response is mocked.
export async function runSDKUICases(helpers, fixture) {
  const { check, device, pageErrors, preserveBrowserFailureState } = helpers
  assert.equal(fixture.config.Chain, 'regtest')
  if (!helpers.selectedCases?.every(name => sdkAccountCases.includes(name))) assert.equal(fixture.realBitcoinControl, true)
  const actors = fixture.uiActors
  const poll = { timeout: 180000, intervals: [500, 1000, 2000] }
  let activeCase = ''
  const calls = []
  const accountNS = 'sat20account_wasm'
  const logNS = 'sat20wallet_operation_log'
  const call = async (page, method, args = [], ns = 'sat20wallet_wasm', fails = false) => {
    let timer
    try {
      const result = await Promise.race([
        page.evaluate(async ({ ns, method, args }) => {
          if (typeof window[ns]?.[method] !== 'function') throw new Error(`Missing WASM method: ${ns}.${method}`)
          return await window[ns][method](...args)
        }, { ns, method, args }),
        new Promise((_, reject) => { timer = setTimeout(() => reject(new Error(`${method} timed out`)), 180000) }),
      ])
      calls.push({ case: activeCase, namespace: ns, method, code: result.code })
      if (fails) {
        assert.notEqual(result.code, 0, `${method} must reject this request`)
        if (typeof fails === 'string') assert.equal(result.msg, fails, `${method} refusal`)
        if (fails instanceof RegExp) assert.match(result.msg, fails, `${method} refusal`)
      }
      else assert.equal(result.code, 0, `${method}: ${result.msg}`)
      return result.data
    } finally { clearTimeout(timer) }
  }
  const rgb = (p, name, input, fails = false) => call(p, name, input === undefined ? [] : [JSON.stringify(input)], undefined, fails)
  const account = (p, name, input = {}, fails = false) => call(p, name, [JSON.stringify(input)], accountNS, fails)
  const text = file => Buffer.from(file.data, 'base64').toString('utf8')
  const recoveryFile = setup => text(setup.files.find(f => f.name === 'user-share.txt'))
  const questions = [
    ['place', 'childhood place', 'violet hill beside the old station'],
    ['note', 'private note', 'yellow bicycle beside the river'],
    ['family', 'family phrase', 'meet under the tree at six'],
  ].map(([id, prompt, answer]) => ({ question: { id, prompt, case_sensitive: false, ignore_punctuation: true }, answer, confirmation: answer }))
  const answers = questions.map(q => ({ question_id: q.question.id, text: q.answer }))
  const control = async (action, body = {}) => {
    const response = await fetch(new URL(`/${action}`, fixture.control_url), {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body), signal: AbortSignal.timeout(240000),
    })
    assert.equal(response.ok, true, `${action}: HTTP ${response.status}`)
    const result = await response.json()
    assert.ok(!result.error, result.error)
    return result
  }
  const l1 = async path => {
    const e = fixture.config.IndexerL1
    const response = await fetch(`${e.Scheme}://${e.Host}/${String(e.Proxy).replace(/^\/+|\/+$/g, '')}${path}`, { signal: AbortSignal.timeout(30000) })
    assert.equal(response.ok, true, `real L1 ${path}: HTTP ${response.status}`)
    return response.json()
  }
  const transaction = async txid => {
    const result = await l1(`/btc/rawtx/${txid}`)
    assert.equal(result.code, 0, result.msg)
    const tx = Transaction.fromHex(result.data)
    assert.equal(tx.getId(), txid)
    return tx
  }
  const mine = () => control('confirm-l1', { wait_anchors: false, empty_blocks: 1 })
  const reload = async (page, password, ready = true) => {
    await page.reload()
    await page.waitForFunction(() => Boolean(window.__SAT20_PWA_VERIFY__), undefined, { timeout: 90000 })
    await call(page, 'unlockWallet', [password])
    if (ready) await accountReady(page)
  }
  const accountReady = async page => {
    await expect.poll(async () => (await account(page, 'getAccountView')).modules
      .some(m => m.name === 'rgb11' && m.can_write), poll).toBe(true)
  }
  // Call the real WASM business boundaries. Matching the synchronization
  // error prevents a bad argument or empty balance from passing this test.
  const gateError = /^(DKVS managed path is not synchronized|account-managed data import is incomplete; restore the wallet before uploading)$/
  const blockedL1 = async (page, actor) => {
    const broadcasts = []
    const observe = req => {
      if (req.method() === 'POST' && /\/(btc\/txs?|bitcoin\/tx\/broadcast)$/.test(new URL(req.url()).pathname)) broadcasts.push(req.url())
    }
    page.on('request', observe)
    try {
      await call(page, 'batchSendAssets', [actor.address, '::', '1000', 1, '2'], 'sat20wallet_wasm', gateError)
      await call(page, 'deployTickerBrc20', ['gate', '10000', '100', '0', '2'], 'sat20wallet_wasm', gateError)
      await rgb(page, 'refreshRGB11State', {}, gateError)
      assert.deepEqual(broadcasts, [], 'blocked operations must never call a broadcast backend')
    } finally { page.off('request', observe) }
  }
  const localDataAvailable = async page => {
    const before = await call(page, 'getWalletCatalog')
    await account(page, 'getAccountView')
    await rgb(page, 'getRGB11View')
    const log = await call(page, 'beginOperationLog', [JSON.stringify({ category: 'wallet', action: 'e2e_local_log', title: 'Local log during synchronization' })], logNS)
    await call(page, 'updateOperationLog', [log.id, JSON.stringify({ status: 'succeeded' })], logNS)
    await call(page, 'getOperationLogs', [], logNS)
    await call(page, 'deleteOperationHistory', [], logNS)
    assert.deepEqual(await call(page, 'getWalletCatalog'), before, 'independent local logs must not change account data')
  }
  const imported = async (page, actor) => {
    const result = await call(page, 'importWallet', [actor.mnemonic, actor.password])
    assert.equal(result.address, actor.address)
    await expect.poll(async () => {
      const view = await account(page, 'getAccountView')
      return view.state === 'local' && view.modules.some(m => m.name === 'rgb11' && m.can_write)
    }, poll).toBe(true)
    return (await call(page, 'getWalletCatalog')).wallets.map(w => w.id)
  }
  const storage = async (page, id) => {
    const options = await account(page, 'getStorageOptions', { record_count: 100 })
    const option = options.find(o => o.id === id)
    assert.equal(option?.available, true, `${id} storage must exist on the real Core`)
    let view = await account(page, 'confirmStorage', option)
    if (view.status === 'pending') {
      const txid = view.tx_id
      assert.match(txid, /^[0-9a-f]{64}$/)
      const payer = Object.values(actors).find(a => a.address === option.payment?.source_address)
      if (payer) {
        await reload(page, payer.password)
        // An active account completes its initial import before accepting new
        // state changes. Continue the original payment only after that gate.
        await accountReady(page)
      }
      await expect.poll(async () => {
        view = await account(page, 'confirmStorage', option)
        if (view.tx_id) assert.equal(view.tx_id, txid, 'continue the original payment, never pay twice')
        return view.status
      }, poll).toBe('ready')
    }
    assert.equal(view.status, 'ready')
    return view
  }
  const cancelSetup = async (page, actor, walletIDs) => {
    await account(page, 'cancelStorageSetup')
    assert.equal((await account(page, 'getAccountView')).storage?.can_cancel ?? false, false)
    await account(page, 'createRecovery', { password: actor.password, wallet_ids: walletIDs,
      mode: '2of2', questions }, 'account storage authorization is unavailable')
  }
  const setup = async (page, actor, walletIDs) => {
    const before = await account(page, 'preflightRecovery', { password: actor.password, wallet_ids: walletIDs })
    assert.equal(before.wallets.length, walletIDs.length)
    return account(page, 'createRecovery', { password: actor.password, wallet_ids: walletIDs, mode: '2of2', questions })
  }
  const activate = async (page, actor, created, factors) => {
    const view = await account(page, 'activateRecovery', { session_id: created.session_id,
      input: { password: actor.password, recovery_file: recoveryFile(created), factors } })
    assert.equal(view.active, true)
    await expect.poll(async () => {
      const v = await account(page, 'refreshAccount')
      return { backup: v.backup_state, writable: v.modules.some(m => m.name === 'rgb11' && m.can_write) }
    }, poll).toEqual({ backup: 'backed_up', writable: true })
    return view
  }
  const localBackup = async (page, actor, walletIDs) => {
    await storage(page, 'temporary')
    const created = await setup(page, actor, walletIDs)
    await activate(page, actor, created, { answers })
    return created
  }
  const issue = (page, kind, ticker) => rgb(page, 'issueRGB11Asset', {
    kind, ticker, name: `SDK E2E ${ticker}`, details: 'Isolated real regtest E2E', precision: 2,
    amounts: kind === 'IFA' ? ['40', '60'] : ['100'], inflation: kind === 'IFA' ? ['10', '10'] : [], reject_url: '',
  })
  const importAsset = async (owner, receiver, asset) => {
    const files = await rgb(owner, 'exportRGB11Contract', { contract_id: asset.contract_id })
    const imported = await rgb(receiver, 'importRGB11Contract', { data: files.find(f => f.name.endsWith('.rgb')).data })
    assert.equal(imported.contract_id, asset.contract_id)
    assert.equal(imported.available, '0')
  }
  const invoice = (page, asset, amount, mode = 'witness', transport = 'manual') => rgb(page, 'createRGB11Invoice', {
    asset: asset.asset, amount, receive_mode: mode, transport, proxy_endpoint: transport === 'proxy' ? fixture.rgbProxy : '',
  })
  const balance = async (page, asset, expected) => {
    await expect.poll(async () => {
      const v = await rgb(page, 'refreshRGB11State')
      assert.equal(v.error_code, '')
      return v.assets.find(a => a.asset === asset.asset)?.available
    }, poll).toBe(expected)
  }
  const manualSend = async (owner, receiver, asset, amount, inflate = false, mode = 'witness', format = '.asc', previous) => {
    const receive = previous?.receive ?? await invoice(receiver, asset, amount, mode)
    const prepared = previous?.prepared ?? (inflate
      ? await rgb(owner, 'prepareRGB11Inflation', { invoice: receive.invoice, fee_rate: 2 })
      : await rgb(owner, 'prepareRGB11Transfer', { recipients: [{ destination: receive.invoice }], fee_rate: 2 }))
    assert.equal(prepared.needs_receipts, true)
    assert.equal(prepared.recipients[0].amount.amount, amount)
    const files = await rgb(owner, 'exportRGB11Transfer', { id: prepared.id })
    await rgb(receiver, 'prepareRGB11Consignment', { id: receive.id, data: Buffer.from('invalid proof').toString('base64') }, true)
    const file = files.find(f => f.file.name.endsWith(format === 'json' ? '.asc' : format)).file
    const data = format === 'json' ? Buffer.from(JSON.stringify({ transport_mode: 'out-of-band', consignment: text(file) })).toString('base64') : file.data
    const received = await rgb(receiver, 'prepareRGB11Consignment', { id: receive.id, data })
    assert.equal(received.can_accept, true)
    assert.ok(received.receipt)
    if (mode === 'blind') {
      await reload(owner, actors.collectible_owner.password)
      await reload(receiver, actors.collectible_receiver.password)
      assert.ok((await rgb(owner, 'getRGB11View')).transfers.some(t => t.id === prepared.id && t.can_confirm))
      assert.ok((await rgb(receiver, 'getRGB11View')).receives.some(r => r.id === receive.id && r.receipt === received.receipt))
    }
    await rgb(owner, 'confirmRGB11Transfer', { id: prepared.id, receipts: ['invalid receipt'] }, true)
    const snapshot = await control('snapshot')
    assert.ok(!(snapshot.l1 ?? snapshot).pending_txids.includes(prepared.tx_id), 'invalid receipt must not broadcast')
    const confirmed = await rgb(owner, 'confirmRGB11Transfer', { id: prepared.id, receipts: [received.receipt] })
    assert.equal(confirmed.id, prepared.id)
    assert.equal(confirmed.tx_id, prepared.tx_id)
    const tx = await transaction(confirmed.tx_id)
    assert.ok(tx.ins.every(input => input.witness.length > 0))
    await mine()
    const accepted = await rgb(receiver, 'acceptRGB11Consignment', { id: receive.id })
    assert.equal(accepted.tx_id, confirmed.tx_id)
    return confirmed
  }
  const paidRecovery = async pages => {
    const ownerActor = actors.account_owner
    const owner = await device(); pages.push(owner)
    const walletIDs = await imported(owner, ownerActor)
    await storage(owner, 'temporary')
    await cancelSetup(owner, ownerActor, walletIDs)
    // Actually back up the unchanged catalog under temporary storage before
    // upgrading it. Selecting and cancelling a quote does not test this case.
    await localBackup(owner, ownerActor, walletIDs)
    const originalCatalog = await call(owner, 'getWalletCatalog')
    const accountID = (await account(owner, 'getAccountView')).account_id
    const endpoint = fixture.config.IndexerL2
    const recordURL = new URL(`${endpoint.Scheme}://${endpoint.Host}/${String(endpoint.Proxy).replace(/^\/+|\/+$/g, '')}/v3/dkvs/record`)
    recordURL.searchParams.set('key', `/personal/${accountID}/account/state`)
    const readModule = async () => {
      const response = await fetch(recordURL, { signal: AbortSignal.timeout(30000) })
      assert.equal(response.ok, true, `Core wallet backup read: HTTP ${response.status}`)
      const result = await response.json()
      assert.equal(result.code, 0, result.msg)
      return result.data
    }
    const temporaryModule = await readModule()
    assert.ok(temporaryModule.TTL > 0)
    const options = await account(owner, 'getStorageOptions', { record_count: 200 })
    const original = options.find(o => o.id === 'paid')
    assert.equal(original.record_count, 200)
    const bad = structuredClone(original); bad.payment.amount.amount = '1'
    await account(owner, 'confirmStorage', bad, true)
    await storage(owner, 'paid')
    const created = await setup(owner, ownerActor, walletIDs)
    const factors = { answers }
    await account(owner, 'activateRecovery', { session_id: created.session_id,
      input: { password: ownerActor.password, recovery_file: 'not-the-saved-code', factors } }, true)
    await activate(owner, ownerActor, created, factors)
    let status = await account(owner, 'getAccountView')
    assert.equal(status.storage.option.id, 'paid')
    assert.deepEqual(await call(owner, 'getWalletCatalog'), originalCatalog)
    const paidModule = await readModule()
    assert.equal(paidModule.Value, temporaryModule.Value, 'exercise a storage policy change with unchanged encrypted module content')
    assert.equal(paidModule.TTL, 0)
    assert.ok(paidModule.Seq > temporaryModule.Seq)
    assert.notEqual(paidModule.FeeProof, temporaryModule.FeeProof)
    writeFileSync(join(process.env.SAT20_REAL_REGTEST_RUNTIME, 'paid-policy.json'),
      JSON.stringify({ account_id: accountID, module_value: temporaryModule.Value }), { mode: 0o600 })
    assert.equal(status.autopay.can_fund, true)
    const quote = status.autopay.quote
    const badQuote = structuredClone(quote); badQuote.amount.amount = '1'
    await account(owner, 'fundAutopay', badQuote, true)
    const renewal = await account(owner, 'fundAutopay', quote)
    assert.match(renewal.tx_id, /^[0-9a-f]{64}$/)
    await reload(owner, ownerActor.password)
    await expect.poll(async () => {
      status = await account(owner, 'refreshAccount')
      return status.autopay.status
    }, poll).toBe('ready')
    const rehearsal = await account(owner, 'loadRecovery', { recovery_file: recoveryFile(created), rehearsal: true })
    const preview = await account(owner, 'previewRecovery', { session_id: rehearsal.session_id, factors })
    assert.equal(preview.can_commit, false); assert.equal(preview.summary.wallets.length, walletIDs.length)
    await account(owner, 'commitRecovery', { session_id: rehearsal.session_id, new_password: ownerActor.password }, true)
    await account(owner, 'abortRecovery', { session_id: rehearsal.session_id })
    const before = await call(owner, 'getWalletCatalog')
    await owner.context().close()
    const restored = await device(); pages.push(restored)
    const loaded = await account(restored, 'loadRecovery', { recovery_file: recoveryFile(created) })
    const ready = await account(restored, 'previewRecovery', { session_id: loaded.session_id, factors })
    assert.equal(ready.can_commit, true)
    const newPassword = `${ownerActor.password}-restored`
    const result = await account(restored, 'commitRecovery', { session_id: loaded.session_id, new_password: newPassword })
    assert.equal(result.account_id, status.account_id)
    await reload(restored, newPassword)
    const after = await call(restored, 'getWalletCatalog')
    assert.deepEqual(after.wallets.map(w => ({ fingerprint: w.fingerprint, name: w.name, accounts: w.accounts })),
      before.wallets.map(w => ({ fingerprint: w.fingerprint, name: w.name, accounts: w.accounts })))
    const coldView = await account(restored, 'getAccountView')
    assert.equal(coldView.account_id, status.account_id)
    assert.equal(coldView.autopay.status, 'ready')
  }
  // Diagnostic continuation only: load the exact failed IndexedDB stores;
  // never create replacement transactions or mark these observations as E2E coverage.
  if (fixture.uiDiagnostic) {
    const observe = async (page, method, input, stage) => {
      const result = await page.evaluate(async ({ method, input }) => window.sat20wallet_wasm[method](...(input === undefined ? [] : [JSON.stringify(input)])), { method, input })
      console.log(JSON.stringify({ diagnostic: 'sdk-ui-resume', stage, method, code: result.code, message: result.msg, status: result.data?.status }))
      return result
    }
    for (const role of ['direct_receiver', 'proxy_owner']) {
      const name = role === 'proxy_owner' ? sdkUICases[6] : sdkUICases[1]
      if (!helpers.selectedCases.includes(name)) continue
      await check(name, async () => {
        const page = await device(undefined, true, undefined, role, fixture.uiResumeStates[role])
        let peer
        try {
          await call(page, 'unlockWallet', [actors[role].password])
          if (role === 'direct_receiver') {
            await page.context().setOffline(true)
            await observe(page, 'refreshRGB11State', undefined, 'offline')
            await page.context().setOffline(false)
            await observe(page, 'refreshRGB11State', undefined, 'online-immediate')
            await account(page, 'refreshAccount')
            await observe(page, 'refreshRGB11State', undefined, 'after-account-refresh')
            await expect.poll(async () => {
              const view = await account(page, 'getAccountView')
              return view.modules.some(m => m.name === 'rgb11' && m.can_write)
            }, poll).toBe(true)
            await observe(page, 'refreshRGB11State', undefined, 'after-account-ready')
          } else {
            const state = await rgb(page, 'getRGB11View')
            assert.equal(state.transfers.length, 1)
            const id = state.transfers[0].id
            await observe(page, 'confirmRGB11Transfer', { id }, 'republish-original')
            peer = await device(undefined, true, undefined, 'proxy_receiver', fixture.uiResumeStates.proxy_receiver)
            await call(peer, 'unlockWallet', [actors.proxy_receiver.password])
            await observe(peer, 'refreshRGB11State', undefined, 'receiver-ack-original')
            await observe(page, 'confirmRGB11Transfer', { id }, 'cold-continue')
            await observe(page, 'refreshRGB11State', undefined, 'explicit-refresh')
            await observe(page, 'confirmRGB11Transfer', { id }, 'after-explicit-refresh')
          }
        } finally {
          await preserveBrowserFailureState?.()
          if (peer) await peer.context().close()
          await page.context().close()
        }
      })
    }
    return
  }
  const restored = async role => {
    const page = await device(undefined, true, undefined, role, fixture.uiResumeStates?.[role])
    if (fixture.uiResumeStates?.[role]) await call(page, 'unlockWallet', [actors[role].password])
    else await imported(page, actors[role])
    return page
  }
  const cases = [
    async pages => {
      const owner = await restored('manual_owner'); pages.push(owner)
      const receiver = await restored('manual_receiver'); pages.push(receiver)
      let asset, previous
      if (fixture.uiResumeStates?.manual_owner) {
        const source = await rgb(owner, 'getRGB11View')
        const target = await rgb(receiver, 'getRGB11View')
        asset = source.assets.find(a => a.asset === source.transfers[0]?.recipients[0]?.amount.asset)
        assert.ok(asset)
        assert.equal(source.transfers.length, 1)
        assert.equal(target.receives.length, 1)
        previous = { prepared: source.transfers[0], receive: target.receives[0] }
        assert.ok(previous.receive.receipt, 'original validated proof must expose its receipt')
      } else {
        asset = await issue(owner, 'IFA', 'UIMAN')
        await importAsset(owner, receiver, asset)
        await balance(owner, asset, '100')
      }
      const sent = await manualSend(owner, receiver, asset, '10.25', false, 'witness', '.asc', previous)
      await balance(receiver, asset, '10.25'); await balance(owner, asset, '89.75')
      await manualSend(owner, receiver, asset, '2.5', true)
      await balance(receiver, asset, '12.75'); await balance(owner, asset, '89.75')
      const request = await invoice(receiver, asset, '1')
      const pending = await rgb(owner, 'prepareRGB11Transfer', { recipients: [{ destination: request.invoice }], fee_rate: 2 })
      const root = (await call(owner, 'getWalletCatalog')).wallets[0].id
      await call(owner, 'ensureAccount', [String(root), 1, 'E2E separate account'])
      await call(owner, 'switchAccount', [1])
      assert.equal((await rgb(owner, 'getRGB11View')).transfers.some(t => t.id === pending.id), false)
      await rgb(owner, 'confirmRGB11Transfer', { id: pending.id }, true)
      await call(owner, 'switchAccount', [0])
      await reload(owner, actors.manual_owner.password)
      await call(owner, 'deleteOperationHistory', [], logNS)
      assert.ok((await rgb(owner, 'getRGB11View')).transfers.some(t => t.id === pending.id && t.can_cancel))
      assert.ok((await call(owner, 'getOperationLogs', [], logNS)).some(l => l.status === 'running'))
      const cancelled = await rgb(owner, 'cancelRGB11Transfer', { id: pending.id })
      assert.equal(cancelled.status, 'cancelled')
      // A cold wallet must read its saved proof/balance without a successful
      // L1 refresh. Abort only L1 transport; no synthetic chain data is supplied.
      const endpoint = fixture.config.IndexerL1
      const prefix = `${endpoint.Scheme}://${endpoint.Host}/${String(endpoint.Proxy).replace(/^\/+|\/+$/g, '')}`.replace(/\/+$/, '')
      const l1Route = url => url.href.startsWith(`${prefix}/`)
      await owner.route(l1Route, route => route.abort('internetdisconnected'))
      try {
        await reload(owner, actors.manual_owner.password)
        const saved = await rgb(owner, 'getRGB11View')
        const holding = saved.assets.find(a => a.asset === asset.asset)
        assert.equal(holding?.available, '89.75', 'cold local RGB balance does not require full chain reconciliation')
        assert.equal(holding?.can_send, true)
      } finally {
        await owner.unroute(l1Route)
      }
      await reload(receiver, actors.manual_receiver.password)
      await balance(owner, asset, '89.75'); await balance(receiver, asset, '12.75')
      const view = await rgb(owner, 'getRGB11View')
      assert.ok(view.transfers.some(t => t.id === sent.id && t.status === 'completed'))
      const files = await rgb(owner, 'exportRGB11Transfer', { id: sent.id })
      assert.ok(files.every(f => f.file.data.length > 0))
      await call(owner, 'deleteOperationHistory', [], logNS)
      assert.equal((await call(owner, 'getOperationLogs', [], logNS)).filter(l => l.status === 'succeeded' || l.status === 'cancelled').length, 0)
      await balance(owner, asset, '89.75')
      // PWA accepts multiple invoice lines and displays one receipt per row.
      const firstRequest = await invoice(receiver, asset, '1.25')
      const blindRequest = await invoice(receiver, asset, '2.5', 'blind')
      await rgb(owner, 'prepareRGB11Transfer', { recipients: [firstRequest, blindRequest].map(r => ({ destination: r.invoice })), fee_rate: 2 }, 'RGB11 batch send requires witness invoices')
      const requests = [firstRequest, await invoice(receiver, asset, '2.5')]
      const batch = await rgb(owner, 'prepareRGB11Transfer', {
        recipients: requests.map(r => ({ destination: r.invoice })), fee_rate: 2,
      })
      assert.equal(batch.recipients.length, 2)
      assert.deepEqual(batch.recipients.map(r => r.amount.amount), ['1.25', '2.5'])
      const packages = await rgb(owner, 'exportRGB11Transfer', { id: batch.id })
      const receipts = []
      for (const [i, request] of requests.entries()) {
        const file = packages.find(f => f.recipient_index === i && f.file.name.endsWith('.asc'))
        assert.ok(file)
        const received = await rgb(receiver, 'prepareRGB11Consignment', { id: request.id, data: file.file.data })
        assert.ok(received.receipt)
        receipts.push(received.receipt)
      }
      await rgb(owner, 'confirmRGB11Transfer', { id: batch.id, receipts: receipts.slice(0, 1) }, true)
      await rgb(owner, 'confirmRGB11Transfer', { id: batch.id, receipts: [receipts[0], receipts[0]] }, true)
      const pendingChain = await control('snapshot')
      assert.ok(!(pendingChain.l1 ?? pendingChain).pending_txids.includes(batch.tx_id), 'incomplete receipts must not broadcast the batch')
      const confirmed = await rgb(owner, 'confirmRGB11Transfer', { id: batch.id, receipts: [...receipts].reverse() })
      assert.equal(confirmed.id, batch.id); assert.equal(confirmed.tx_id, batch.tx_id)
      await mine()
      for (const request of requests) {
        const received = await rgb(receiver, 'acceptRGB11Consignment', { id: request.id })
        assert.equal(received.tx_id, batch.tx_id)
      }
      await reload(owner, actors.manual_owner.password); await reload(receiver, actors.manual_receiver.password)
      await balance(owner, asset, '86'); await balance(receiver, asset, '16.5')

      // Device reset keeps the remote account. Do not call deleteWallet or
      // copy IndexedDB, contracts or consignments into the restored device.
      const catalog = await call(owner, 'getWalletCatalog')
      await localBackup(owner, actors.manual_owner, catalog.wallets.map(w => w.id))
      const sourceAccount = await account(owner, 'getAccountView')
      assert.equal(sourceAccount.backup_state, 'backed_up')
      const rgbState = view => ({
        assets: view.assets.map(a => ({
          asset: a.asset, contract_id: a.contract_id, kind: a.kind, ticker: a.ticker,
          name: a.name, precision: a.precision, available: a.available, pending: a.pending,
          can_send: a.can_send, can_inflate: a.can_inflate,
        })).sort((a, b) => a.asset.localeCompare(b.asset)),
        transfers: view.transfers.map(t => ({
          id: t.id, recipients: t.recipients, status: t.status, tx_id: t.tx_id,
        })).sort((a, b) => a.id.localeCompare(b.id)),
        receives: view.receives.map(r => ({
          id: r.id, amount: r.amount, status: r.status, tx_id: r.tx_id,
        })).sort((a, b) => a.id.localeCompare(b.id)),
      })
      const beforeReset = rgbState(await rgb(owner, 'getRGB11View'))
      assert.equal(beforeReset.assets.find(a => a.asset === asset.asset)?.can_inflate, true)
      await owner.context().close()
      const recovered = await device(undefined, true, undefined, 'manual_owner_restored'); pages.push(recovered)
      assert.equal((await call(recovered, 'getWalletCatalog')).wallets.length, 0, 'new device must have no local wallet')
      const password = `${actors.manual_owner.password}-restored`
      const result = await call(recovered, 'importWallet', [actors.manual_owner.mnemonic, password])
      assert.equal(result.address, actors.manual_owner.address)
      await expect.poll(async () => {
        const v = await account(recovered, 'refreshAccount')
        return { active: v.active, account: v.account_id, backup: v.backup_state,
          writable: v.modules.some(m => m.name === 'rgb11' && m.can_write) }
      }, poll).toEqual({ active: true, account: sourceAccount.account_id, backup: 'backed_up', writable: true })
      const walletState = c => c.wallets.map(w => ({
        name: w.name, fingerprint: w.fingerprint, accounts: w.accounts,
      })).sort((a, b) => a.fingerprint.localeCompare(b.fingerprint))
      assert.deepEqual(walletState(await call(recovered, 'getWalletCatalog')), walletState(catalog))
      const afterReset = rgbState(await rgb(recovered, 'getRGB11View'))
      assert.deepEqual(afterReset.assets, beforeReset.assets, 'remote backup must restore RGB allocations and inflation authority')
      // Spend the restored allocation and exercise the remaining IFA authority.
      const resent = await manualSend(recovered, receiver, asset, '1.25')
      assert.ok(!beforeReset.transfers.some(t => t.tx_id === resent.tx_id))
      await balance(recovered, asset, '84.75'); await balance(receiver, asset, '17.75')
      const inflated = await manualSend(recovered, receiver, asset, '2.5', true)
      await balance(recovered, asset, '84.75'); await balance(receiver, asset, '20.25')
      await backedUp(recovered)
      await reload(recovered, password)
      await balance(recovered, asset, '84.75'); await balance(receiver, asset, '20.25')
      evidence('rgb-device-recovery', { device_reset: true, remote_account_preserved: true,
        catalog_restored: true, assets_restored: true, asset: asset.asset, contract_id: asset.contract_id,
        before_owner: '86', before_receiver: '16.5', after_owner: '84.75', after_receiver: '20.25',
        restored_transfers: afterReset.transfers.map(t => ({ id: t.id, status: t.status })),
        send_tx_id: resent.tx_id, inflation_tx_id: inflated.tx_id })
      // Keep the full equality requirement, after collecting independent proof
      // that restored assets can actually be spent and inflated.
      assert.deepEqual(afterReset, beforeReset, 'remote backup must restore the complete RGB business state')
    },
    async pages => {
      const owner = await device(); pages.push(owner)
      const receiver = await device(); pages.push(receiver)
      const ownerIDs = await imported(owner, actors.direct_owner)
      const receiverIDs = await imported(receiver, actors.direct_receiver)
      await localBackup(owner, actors.direct_owner, ownerIDs)
      await localBackup(receiver, actors.direct_receiver, receiverIDs)
      const asset = await issue(owner, 'NIA', 'UIDIR')
      await importAsset(owner, receiver, asset)
      await balance(receiver, asset, '0')
      await receiver.context().setOffline(true)
      const prepared = await rgb(owner, 'prepareRGB11Transfer', { recipients: [{ destination: actors.direct_receiver.address, asset: asset.asset, amount: '10.25' }], fee_rate: 2 })
      const delivered = await rgb(owner, 'confirmRGB11Transfer', { id: prepared.id })
      assert.equal(delivered.id, prepared.id)
      assert.ok(['awaiting_ack', 'awaiting_persistence'].includes(delivered.status), 'ordinary Direct still requires receiver ACK')
      await owner.context().setOffline(true)
      await receiver.context().setOffline(false)
      await accountReady(receiver)
      await rgb(receiver, 'refreshRGB11State')
      // The receiving wallet persists its proof/ACK, then goes offline before
      // the sender can observe ACK and broadcast. Public withdrawals differ.
      await receiver.context().setOffline(true)
      await owner.context().setOffline(false)
      await accountReady(owner)
      let broadcast
      await expect.poll(async () => {
        broadcast = await rgb(owner, 'confirmRGB11Transfer', { id: prepared.id })
        return broadcast.status
      }, poll).toBe('pending_chain')
      assert.equal(broadcast.tx_id, prepared.tx_id)
      await mine()
      const tx = await transaction(broadcast.tx_id)
      const ownVout = tx.outs.findIndex(o => o.script.toString('hex') === actors.direct_receiver.pk_script)
      assert.ok(ownVout >= 0)
      const ownPoint = `${broadcast.tx_id}:${ownVout}`
      const otherVout = tx.outs.findIndex(o => o.script.toString('hex') === actors.direct_owner.pk_script)
      assert.ok(otherVout >= 0, 'the same transaction must have the sender RGB change')
      const otherPoint = `${broadcast.tx_id}:${otherVout}`
      await balance(owner, asset, '89.75')
      // Spend the actual RGB change through a second valid RGB transfer. It is
      // not plain BTC and must not be bypassed with a garbage-output send.
      const spent = await manualSend(owner, owner, asset, '1')
      const spendingTx = await transaction(spent.tx_id)
      assert.ok(spendingTx.ins.some(i => `${Buffer.from(i.hash).reverse().toString('hex')}:${i.index}` === otherPoint))
      const own = await l1(`/v3/utxo/info/${ownPoint}`)
      assert.equal(own.code, 0); assert.ok(own.data)
      const other = await l1(`/v3/utxo/info/${otherPoint}`)
      assert.ok(other.code !== 0 || !other.data, 'the other output must really be spent')
      await receiver.context().setOffline(false)
      await reload(receiver, actors.direct_receiver.password)
      await accountReady(receiver)
      await balance(receiver, asset, '10.25')
      // A further real block during refresh must not invalidate the receipt.
      await Promise.all([mine(), rgb(receiver, 'refreshRGB11State')])
      await balance(receiver, asset, '10.25')
    },
    pages => paidRecovery(pages),
  ]
  cases.push(async pages => {
    const owner = await device(); pages.push(owner)
    const actor = actors.recovery_owner
    await imported(owner, actor)
    const first = (await call(owner, 'getWalletCatalog')).wallets[0].id
    await call(owner, 'ensureAccount', [String(first), 1, 'Backup account 1'])
    await call(owner, 'createWallet', [actor.password])
    await call(owner, 'switchWallet', [String(first), actor.password])
    const walletIDs = (await call(owner, 'getWalletCatalog')).wallets.map(w => w.id)
    assert.equal(walletIDs.length, 2)
    await account(owner, 'preflightRecovery', { password: 'wrong-password', wallet_ids: walletIDs }, true)
    const exports = await owner.evaluate(() => Object.keys(window.sat20account_wasm).sort())
    assert.deepEqual(exports, ['getAccountView', 'refreshAccount', 'preflightRecovery',
      'getStorageOptions', 'confirmStorage', 'activateRootAccount', 'cancelStorageSetup', 'fundAutopay',
      'getGuardianContact', 'createRecovery', 'acceptGuardianSetup', 'confirmGuardianSetup',
      'activateRecovery', 'loadRecovery', 'previewRecovery', 'createGuardianRequest',
      'createGuardianResponse', 'commitRecovery', 'abortRecovery'].sort())
    await storage(owner, 'temporary')
    await cancelSetup(owner, actor, walletIDs)
    await storage(owner, 'temporary')
    const abandoned = await setup(owner, actor, walletIDs)
    await account(owner, 'abortRecovery', { session_id: abandoned.session_id })
    await account(owner, 'activateRecovery', { session_id: abandoned.session_id,
      input: { password: actor.password, recovery_file: recoveryFile(abandoned),
        factors: { answers } } }, 'activation session expired')
    await storage(owner, 'temporary')
    const created = await setup(owner, actor, walletIDs)
    await activate(owner, actor, created, { answers })
    const status = await account(owner, 'getAccountView')
    assert.equal(status.backup_risk, '')
    await owner.context().setOffline(true)
    await call(owner, 'ensureAccount', [String(first), 2, 'Created while offline'])
    assert.equal((await account(owner, 'getAccountView')).backup_risk, 'unconfirmed')
    // The existing worker must actually observe transport failure. A later
    // local write remains allowed after that failed upload/sync attempt.
    await account(owner, 'refreshAccount')
    await expect.poll(async () => (await account(owner, 'getAccountView')).error_code, poll).not.toBe('')
    assert.equal((await account(owner, 'getAccountView')).modules.find(m => m.name === 'rgb11').can_write, true)
    await call(owner, 'ensureAccount', [String(first), 3, 'Created after upload failure'])
    await owner.context().setOffline(false)
    await expect.poll(async () => { const v = await account(owner, 'refreshAccount'); return [v.backup_state, v.backup_risk] }, poll).toEqual(['backed_up', ''])
    // A fresh runtime must complete its first receive. Block only DKVS
    // transport, leaving the app, L1 indexer and independent local data live.
    const offlineDKVS = route => route.abort('internetdisconnected')
    await owner.context().route('**/v3/dkvs/**', offlineDKVS)
    await reload(owner, actor.password, false)
    assert.equal((await account(owner, 'getAccountView')).modules.some(m => m.name === 'rgb11' && m.can_write), false)
    await blockedL1(owner, actor)
    await call(owner, 'ensureAccount', [String(first), 4, 'After initial sync'], 'sat20wallet_wasm', gateError)
    await call(owner, 'batchSendAssets_SatsNet', [actor.address, '::', '1000', 1], 'sat20wallet_wasm', gateError)
    await localDataAvailable(owner)
    await owner.context().unroute('**/v3/dkvs/**', offlineDKVS)
    await account(owner, 'refreshAccount')
    await accountReady(owner)
    await call(owner, 'ensureAccount', [String(first), 4, 'After initial sync'])
    await rgb(owner, 'refreshRGB11State')
    await backedUp(owner)
    evidence('account-sync-gate', { cold_sync_blocked: true, local_data_available: true, upload_only_allowed: true, resumed_after_sync: true })
    const before = await call(owner, 'getWalletCatalog')
    writeFileSync(join(process.env.SAT20_REAL_REGTEST_RUNTIME, 'two-factor-material.private.json'),
      JSON.stringify({ created, status, before }), { mode: 0o600 })
    await owner.context().close()
    const restored = await device(); pages.push(restored)
    await account(restored, 'loadRecovery', { recovery_file: 'invalid recovery package' }, true)
    let loaded = await account(restored, 'loadRecovery', { recovery_file: recoveryFile(created) })
    await account(restored, 'commitRecovery', { session_id: loaded.session_id,
      new_password: actor.password }, 'account recovery preview is required')
    const factors = { answers }
    await account(restored, 'previewRecovery', { session_id: loaded.session_id, factors: { answers: answers.map(a => ({ ...a, text: 'wrong answer' })) } }, true)
    let preview = await account(restored, 'previewRecovery', { session_id: loaded.session_id, factors })
    assert.equal(preview.can_commit, true)
    assert.equal(preview.summary.wallets.length, walletIDs.length)
    // Cancelling a verified preview must not leave a usable commit capability.
    await account(restored, 'abortRecovery', { session_id: loaded.session_id })
    await account(restored, 'commitRecovery', { session_id: loaded.session_id,
      new_password: actor.password }, 'recovery session expired')
    loaded = await account(restored, 'loadRecovery', { recovery_file: recoveryFile(created) })
    await account(restored, 'previewRecovery', { session_id: loaded.session_id, factors })
    await restored.reload()
    await restored.waitForFunction(() => Boolean(window.__SAT20_PWA_VERIFY__), undefined, { timeout: 90000 })
    await account(restored, 'commitRecovery', { session_id: loaded.session_id,
      new_password: actor.password }, 'recovery session expired')
    loaded = await account(restored, 'loadRecovery', { recovery_file: recoveryFile(created) })
    preview = await account(restored, 'previewRecovery', { session_id: loaded.session_id, factors })
    assert.equal(preview.can_commit, true)
    const newPassword = `${actor.password}-restored`
    const result = await account(restored, 'commitRecovery', { session_id: loaded.session_id, new_password: newPassword })
    assert.equal(result.account_id, status.account_id)
    await account(restored, 'commitRecovery', { session_id: loaded.session_id,
      new_password: newPassword }, 'recovery session expired')
    await reload(restored, newPassword)
    const after = await call(restored, 'getWalletCatalog')
    assert.deepEqual(after.wallets.map(w => ({ fingerprint: w.fingerprint, name: w.name, accounts: w.accounts })),
      before.wallets.map(w => ({ fingerprint: w.fingerprint, name: w.name, accounts: w.accounts })))
    assert.equal((await account(restored, 'getAccountView')).account_id, status.account_id)
  })
  cases.push(async pages => {
    const owner = await device(); pages.push(owner)
    const actor = actors.guardian_owner
    const ids = await imported(owner, actor)
    const before = await call(owner, 'getWalletCatalog')
    for (const method of ['getGuardianContact', 'acceptGuardianSetup', 'confirmGuardianSetup',
      'createGuardianRequest', 'createGuardianResponse']) {
      await account(owner, method, { password: actor.password }, 'guardian recovery is disabled')
    }
    await storage(owner, 'temporary')
    await account(owner, 'createRecovery', { password: actor.password, wallet_ids: ids,
      mode: '2of3', questions }, 'guardian recovery is disabled')
    // Refusal must not consume the storage authorization or change the catalog.
    const created = await setup(owner, actor, ids)
    assert.equal(created.can_activate, true)
    await account(owner, 'abortRecovery', { session_id: created.session_id })
    assert.deepEqual(await call(owner, 'getWalletCatalog'), before)
    const view = await account(owner, 'getAccountView')
    assert.equal(view.recovery_mode, '')
    assert.equal(view.autopay, null)
  })

  cases.push(async pages => {
    const owner = await restored('collectible_owner'); pages.push(owner)
    const receiver = await restored('collectible_receiver'); pages.push(receiver)
    const asset = fixture.uiResumeStates?.collectible_owner
      ? (await rgb(owner, 'getRGB11View')).assets.find(a => a.kind === 'NIA')
      : await issue(owner, 'NIA', 'UIBLIND')
    assert.ok(asset)
    const files = await rgb(owner, 'exportRGB11Contract', { contract_id: asset.contract_id })
    await rgb(receiver, 'importRGB11Contract', { data: Buffer.from('invalid contract').toString('base64') }, true)
    const importedAsset = await rgb(receiver, 'importRGB11Contract', { data: files.find(f => f.name.endsWith('.asc')).data })
    assert.equal(importedAsset.contract_id, asset.contract_id)
    await rgb(receiver, 'createRGB11Invoice', { asset: asset.asset, amount: '0', receive_mode: 'blind', transport: 'manual' }, true)
    await rgb(owner, 'prepareRGB11Transfer', { recipients: [{ destination: 'invalid address', asset: asset.asset, amount: '1' }], fee_rate: 2 }, true)
    await manualSend(owner, receiver, asset, '10.25', false, 'blind', '.rgb')
    await balance(owner, asset, '89.75'); await balance(receiver, asset, '10.25')
    const uda = await rgb(owner, 'issueRGB11Asset', { kind: 'UDA', ticker: 'UIUNIQUE', name: 'SDK unique', details: 'E2E unique asset', precision: 0, amounts: ['1'], inflation: [] })
    assert.equal(uda.kind, 'UDA'); assert.equal(uda.available, '1'); assert.equal(uda.can_inflate, false)
    await importAsset(owner, receiver, uda)
    await manualSend(owner, receiver, uda, '1', false, 'blind', 'json')
    await balance(owner, uda, '0'); await balance(receiver, uda, '1')
    await reload(receiver, actors.collectible_receiver.password)
    await balance(receiver, asset, '10.25'); await balance(receiver, uda, '1')
  })
  cases.push(async pages => {
    const owner = await device(); pages.push(owner)
    const receiver = await device(); pages.push(receiver)
    await imported(owner, actors.proxy_owner); await imported(receiver, actors.proxy_receiver)
    const asset = await issue(owner, 'NIA', 'UIPROXY')
    await importAsset(owner, receiver, asset)
    for (const [mode, amount, total] of [['witness', '10.25', '10.25'], ['blind', '2.5', '12.75']]) {
      const receive = await invoice(receiver, asset, amount, mode, 'proxy')
      const prepared = await rgb(owner, 'prepareRGB11Transfer', { recipients: [{ destination: receive.invoice }], fee_rate: 2 })
      assert.equal(prepared.needs_receipts, false)
      await receiver.context().setOffline(true)
      const delivered = await rgb(owner, 'confirmRGB11Transfer', { id: prepared.id })
      assert.equal(delivered.status, 'awaiting_ack')
      const snapshot = await control('snapshot')
      assert.ok(!(snapshot.l1 ?? snapshot).pending_txids.includes(prepared.tx_id))
      await reload(owner, actors.proxy_owner.password)
      await receiver.context().setOffline(false)
      await rgb(receiver, 'refreshRGB11State')
      await expect.poll(async () => (await rgb(owner, 'confirmRGB11Transfer', { id: prepared.id })).status, poll).toBe('pending_chain')
      await mine()
      await balance(receiver, asset, total)
      const view = await rgb(receiver, 'getRGB11View')
      assert.ok(view.receives.some(r => r.id === receive.id && r.status === 'completed'))
      // Wait for the sender's existing monitor, using only the read view.
      // Receiver confirmation alone does not make sender change spendable.
      await expect.poll(async () => (await rgb(owner, 'getRGB11View')).assets.find(a => a.asset === asset.asset)?.available, poll)
        .toBe(total === '10.25' ? '89.75' : '87.25')
    }
    await balance(owner, asset, '87.25')
    await reload(receiver, actors.proxy_receiver.password)
    await balance(receiver, asset, '12.75')
  })
  cases.push(async pages => {
    const owner = await device(); pages.push(owner)
    await imported(owner, actors.did_owner)
    const name = `ui${Date.now().toString(36)}.sats`
    assert.equal((await call(owner, 'checkDIDName', ['bad name!'])).status, 'invalid')
    assert.equal((await call(owner, 'checkDIDName', [name])).status, 'available')
    await call(owner, 'inscribeName', [name, '2'])
    assert.equal((await call(owner, 'checkDIDName', [name])).status, 'submitted')
    await call(owner, 'inscribeName', [name, '2'], undefined, true)
    await mine()
    await expect.poll(async () => (await call(owner, 'checkDIDName', [name])).status, poll).toBe('registered')
    const logs = await call(owner, 'getOperationLogs', [], logNS)
    assert.ok(logs.some(l => l.kind === 'inscribe_name' && l.tx_ids.length > 0))
    await call(owner, 'deleteOperationHistory', [], logNS)
    assert.equal((await call(owner, 'checkDIDName', [name])).status, 'registered')
  })
  const evidence = (name, data) => writeFileSync(join(process.env.SAT20_REAL_REGTEST_RUNTIME, `${name}.json`), JSON.stringify(data), { mode: 0o600 })
  const probeAccount = (page, method, input) => page.evaluate(async ({ method, input }) => window.sat20account_wasm[method](JSON.stringify(input)), { method, input })
  const backedUp = async page => {
    await expect.poll(async () => (await account(page, 'refreshAccount')).backup_state, poll).toBe('backed_up')
  }
  const reimport = async (pages, pending) => {
    const actor = actors[pending ? 'pending_owner' : 'deleted_owner']
    const leaf = actors[pending ? 'pending_leaf' : 'deleted_leaf']
    const owner = await device(); pages.push(owner)
    const rootIDs = await imported(owner, actor)
    await call(owner, 'importWallet', [leaf.mnemonic, actor.password])
    let catalog = await call(owner, 'getWalletCatalog')
    const child = catalog.wallets.find(w => !rootIDs.includes(w.id))
    assert.ok(child)
    await call(owner, 'updateWalletName', [String(child.id), 'Deleted wallet metadata'])
    await call(owner, 'updateAccountMetadata', [String(child.id), 0, 'Deleted account metadata', 'did:deleted:0'])
    await call(owner, 'ensureAccount', [String(child.id), 1, 'Deleted second account', 'did:deleted:1'])
    await call(owner, 'switchWallet', [String(child.id), actor.password])
    await rgb(owner, 'importRGB11Contract', { data: fixture.reviewContract.data })
    const asset = (await rgb(owner, 'getRGB11View')).assets.find(a => a.contract_id === fixture.reviewContract.contract_id)
    assert.ok(asset)
    await call(owner, 'switchWallet', [String(rootIDs[0]), actor.password])
    const created = await localBackup(owner, actor, catalog.wallets.map(w => w.id))
    let blockedWrites = 0
    const routeName = '**/v3/dkvs/records/batch-cas'
    const block = async route => { blockedWrites++; await route.abort('failed') }
    if (pending) await owner.route(routeName, block)
    let originalReceive
    try {
      await call(owner, 'switchWallet', [String(child.id), actor.password])
      originalReceive = await invoice(owner, asset, '1')
      await call(owner, 'switchWallet', [String(rootIDs[0]), actor.password])
      if (!pending) await backedUp(owner)
      await call(owner, 'deleteWallet', [String(child.id)])
      assert.equal((await call(owner, 'getWalletCatalog')).wallets.some(w => w.fingerprint === child.fingerprint), false)
      if (pending) await expect.poll(() => blockedWrites, poll).toBeGreaterThan(0)
      else { await backedUp(owner); await reload(owner, actor.password); await accountReady(owner) }
      await call(owner, 'importWallet', [leaf.mnemonic, actor.password])
    } finally { if (pending) await owner.unroute(routeName, block) }
    catalog = await call(owner, 'getWalletCatalog')
    const returned = catalog.wallets.find(w => w.fingerprint === child.fingerprint)
    assert.ok(returned, 'the same key creates a new wallet')
    assert.notEqual(returned.id, child.id, 'reimport must allocate a new local wallet ID')
    assert.notEqual(returned.name, 'Deleted wallet metadata')
    assert.equal(returned.accounts.length, 1, 'old subaccounts must not be restored')
    assert.notEqual(returned.accounts[0].name, 'Deleted account metadata')
    assert.equal(returned.accounts[0].did || '', '')
    assert.equal(returned.accounts[0].sp_enabled, false)
    await call(owner, 'switchWallet', [String(returned.id), actor.password])
    await accountReady(owner)
    const local = await rgb(owner, 'getRGB11View')
    assert.deepEqual(local.assets, [], 'new wallet must not inherit the deleted contract')
    assert.deepEqual(local.receives, [], 'new wallet must not inherit deleted receive requests')
    await backedUp(owner)
    await reload(owner, actor.password)
    await accountReady(owner)
    const clean = await rgb(owner, 'getRGB11View')
    assert.deepEqual(clean.assets, [], 'late backup or deletion ACK must not restore old assets')
    assert.deepEqual(clean.receives, [], 'old invoices must stay absent after synchronization and restart')
    assert.equal((await call(owner, 'getWalletCatalog')).wallets.find(w => w.id === returned.id)?.accounts.length, 1)
    // Only now explicitly add new module data; it must survive old cleanup jobs.
    await rgb(owner, 'importRGB11Contract', { data: fixture.reviewContract.data })
    const newAsset = (await rgb(owner, 'getRGB11View')).assets.find(a => a.contract_id === fixture.reviewContract.contract_id)
    assert.ok(newAsset)
    const nextReceive = await invoice(owner, newAsset, '2')
    await backedUp(owner)
    evidence(`reimport-${pending ? 'pending' : 'confirmed'}`, { blocked_writes: blockedWrites, new_wallet_id: returned.id !== child.id, old_data_absent: true })
    await preserveBrowserFailureState?.()
    await owner.context().close()
    const target = await device(); pages.push(target)
    const loaded = await account(target, 'loadRecovery', { recovery_file: recoveryFile(created) })
    await account(target, 'previewRecovery', { session_id: loaded.session_id, factors: { answers } })
    await account(target, 'commitRecovery', { session_id: loaded.session_id, new_password: actor.password })
    await reload(target, actor.password)
    const restored = (await call(target, 'getWalletCatalog')).wallets.find(w => w.fingerprint === child.fingerprint)
    assert.ok(restored, 'the new wallet must survive cold recovery')
    assert.equal(restored.accounts.length, 1)
    assert.equal(restored.name, returned.name)
    assert.equal(restored.accounts[0].did || '', '')
    await call(target, 'switchWallet', [String(restored.id), actor.password])
    const view = await rgb(target, 'getRGB11View')
    assert.ok(view.assets.some(a => a.contract_id === fixture.reviewContract.contract_id), 'reimported wallet RGB module must be backed up and cold-restored')
    assert.equal(view.receives.some(r => r.id === originalReceive.id), false, 'deleted receive data must never return from recovery')
    assert.ok(view.receives.some(r => r.id === nextReceive.id), 'new receive data must survive backup and recovery')
  }
  cases.push(pages => reimport(pages, true))
  cases.push(pages => reimport(pages, false))
  cases.push(async pages => {
    assert.equal(process.env.SAT20_UI_RESTORE_FAULT, '1', 'use the explicit module-import failure build')
    const actor = actors.partial_owner
    const owner = await device(); pages.push(owner)
    const ids = await imported(owner, actor)
    const created = await localBackup(owner, actor, ids)
    await owner.context().close()
    const target = await device(); pages.push(target)
    await target.goto(target.url().split('#')[0] + '#/restore-account')
    await target.waitForFunction(() => Boolean(window.__SAT20_PWA_VERIFY__))
    const messages = ['en', 'zh'].map(lang => JSON.parse(readFileSync(new URL(`../../locales/${lang}.json`, import.meta.url), 'utf8')).accountManagement.ui.restoreSuccess)
    await target.evaluate(messages => {
      const original = window.sat20account_wasm.commitRecovery
      window.sat20account_wasm.commitRecovery = async (...args) => {
        const result = await original(...args)
        sessionStorage.setItem('e2e-partial-result', JSON.stringify(result))
        return result
      }
      new MutationObserver(() => {
        if ([...document.querySelectorAll('h2')].some(node => messages.includes(node.textContent.trim()))) sessionStorage.setItem('e2e-partial-success', 'true')
      }).observe(document.body, { childList: true, subtree: true, characterData: true })
    }, messages)
    await target.locator('section textarea').fill(recoveryFile(created))
    await target.locator('section button').click()
    await expect(target.locator('section input[type=password]')).toHaveCount(questions.length)
    for (const [i, question] of questions.entries()) await target.locator('section input[type=password]').nth(i).fill(question.answer)
    await target.locator('section button').click()
    await target.locator('#restore-local-password').fill(actor.password)
    await target.locator('#restore-confirm-password').fill(actor.password)
    await target.locator('section button').click()
    await target.waitForFunction(() => sessionStorage.getItem('e2e-partial-result') !== null)
    const result = await target.evaluate(() => JSON.parse(sessionStorage.getItem('e2e-partial-result')))
    await expect(target.getByTestId('partial-recovery')).toBeVisible()
    assert.equal(result.data?.complete, false)
    assert.ok(result.data.failed_modules.some(module => module.name === 'rgb11'))
    await expect(target.locator('#restore-local-password')).toHaveCount(0)
    const success = await target.evaluate(() => sessionStorage.getItem('e2e-partial-success') === 'true')
    await reload(target, actor.password, false)
    await expect.poll(async () => {
      const view = await account(target, 'getAccountView')
      const module = view.modules.find(m => m.name === 'rgb11')
      return module?.can_write === false && Boolean(module.error_code)
    }, poll).toBe(true)
    const state = await account(target, 'getAccountView')
    const module = state.modules.find(m => m.name === 'rgb11')
    evidence('partial-recovery', { code: result.code, result_fields: Object.keys(result.data || {}), full_success_ui: success,
      module_writable: module?.can_write, module_error: module?.error_code, account_state: state.state })
    await blockedL1(target, actor)
    await localDataAvailable(target)
    assert.equal(module?.can_write, false, 'persisted failed module must stay write-protected')
    assert.ok(module?.error_code, 'failure must survive restart')
    assert.equal(success, false, 'partial module restore must not display overall recovery success')
    assert.ok(result.code !== 0 || result.data?.complete === false, 'commit must report incomplete recovery')
  })

  assert.equal(cases.length, sdkUICases.length)
  for (const [i, run] of cases.entries()) {
    if (helpers.selectedCases && !helpers.selectedCases.includes(sdkUICases[i])) continue
    activeCase = sdkUICases[i]
    const pages = []
    try { await check(sdkUICases[i], () => run(pages)) }
    catch (error) { await preserveBrowserFailureState?.(); throw error }
    finally {
      writeFileSync(join(process.env.SAT20_REAL_REGTEST_RUNTIME, `sdk-ui-${i}-coverage.json`), JSON.stringify(calls.filter(c => c.case === activeCase)), { mode: 0o600 })
      for (const page of pages) if (!page.isClosed()) await page.context().close()
    }
  }
  assert.deepEqual(pageErrors, [])
}
