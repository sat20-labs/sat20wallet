import { chromium } from '@playwright/test'
import { readFileSync } from 'node:fs'

const CDP = process.env.SAT20_CDP_URL || 'http://127.0.0.1:9223'
const PWA_URL = process.env.SAT20_PWA_URL || 'http://localhost:5173/#/'
const PASSWORD = process.env.SAT20_TEST_PASSWORD || '123456'
const ACTION = process.env.SAT20_IRIS_ACTION || 'status'
const IRIS_INVOICE = process.env.SAT20_IRIS_INVOICE || ''
const EXISTING_ASSET_NAME = process.env.SAT20_IRIS_ASSET_NAME || ''
const SEND_AMOUNT = process.env.SAT20_IRIS_SEND_AMOUNT || '10'
const RETURN_AMOUNT = process.env.SAT20_IRIS_RETURN_AMOUNT || '2'
const RETURN_INVOICE_MODE = process.env.SAT20_IRIS_RETURN_MODE || 'witness'
const ISSUE_AMOUNT = process.env.SAT20_IRIS_ISSUE_AMOUNT || '100'
const IRIS_BTC_ADDRESS = process.env.SAT20_IRIS_BTC_ADDRESS || ''
const IRIS_BTC_AMOUNT = process.env.SAT20_IRIS_BTC_AMOUNT || '12000'
const IRIS_PROXY_ENDPOINT = process.env.SAT20_IRIS_PROXY_ENDPOINT || 'rpcs://proxy.iriswallet.com/0.2/json-rpc'
const CONTRACT_FILE_PATH = process.env.SAT20_RGB11_CONTRACT_FILE || ''
const CONTRACT_FILE_BASE64 = CONTRACT_FILE_PATH
  ? readFileSync(CONTRACT_FILE_PATH).toString('base64')
  : ''
const SENDER_MNEMONIC = process.env.SAT20_IRIS_SENDER_MNEMONIC
  || 'inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire'
const SENDER_ADDRESS = process.env.SAT20_IRIS_SENDER_ADDRESS
  || 'tb1p339xkycqwld32maj9eu5vugnwlqxxfef3dx8umse5m42szx3n6aq6qv65g'
const SENDER_WALLET_ID = process.env.SAT20_IRIS_SENDER_WALLET_ID || ''
const CHECKPOINT_KEY = '__sat20_rgb11_iris_checkpoint'

const browser = await chromium.connectOverCDP(CDP)
const context = browser.contexts()[0] || await browser.newContext()
const pwaOrigin = new URL(PWA_URL).origin
const page = context.pages().find((candidate) => candidate.url().startsWith(`${pwaOrigin}/`))
  || await context.newPage()

page.on('console', (message) => {
  if (message.type() === 'error' || message.text().includes('[RGB11 Iris]')) {
    console.error(`[page:${message.type()}] ${message.text()}`)
  }
})
page.on('pageerror', (error) => console.error(`[page:error] ${error.message}`))

await page.goto(PWA_URL, { waitUntil: 'domcontentloaded' })
await page.waitForFunction(() => Boolean(window.__SAT20_PWA_VERIFY__), null, { timeout: 180_000 })

const result = await page.evaluate(async ({
  action, password, senderMnemonic, senderAddress, senderWalletId, irisInvoice, existingAssetName,
  issueAmount, sendAmount, returnAmount,
  returnInvoiceMode,
  irisBtcAddress, irisBtcAmount,
  irisProxyEndpoint,
  contractFileBase64, checkpointKey,
}) => {
  const verify = window.__SAT20_PWA_VERIFY__
  const wallet = verify.useWalletStore()
  const { Chain, Network, sat20, walletStorage } = verify
  const unwrap = (tuple, operation) => {
    if (tuple?.[0]) throw new Error(`${operation}: ${tuple[0].message || String(tuple[0])}`)
    return tuple?.[1]
  }
  const parseState = async () => {
    const result = await unwrap(await sat20.getRGB11State(), 'getRGB11State')
    return JSON.parse(result.state)
  }
  const assetName = (name) => `${name?.Protocol || ''}:${name?.Type || ''}:${name?.Ticker || ''}`
  const assetAmount = (assets, expectedName) => String(
    (assets || []).find((asset) => assetName(asset?.Name) === expectedName)?.Amount?.Value ?? '0',
  )
  const readCheckpoint = () => {
    const raw = localStorage.getItem(checkpointKey)
    return raw ? JSON.parse(raw) : {}
  }
  const writeCheckpoint = (value) => localStorage.setItem(checkpointKey, JSON.stringify(value))
  const waitForData = async () => {
    const deadline = Date.now() + 120_000
    while (Date.now() < deadline) {
      const state = await parseState()
      if (['synced', 'not_configured', 'offline'].includes(state.backup_status)) return state
      if (state.backup_status === 'conflict') throw new Error('wallet data conflict')
      await new Promise((resolve) => setTimeout(resolve, 1_000))
    }
    throw new Error('wallet data synchronization timed out')
  }
  const retryProxyReceive = async (requestID) => {
    const deadline = Date.now() + 10 * 60_000
    while (Date.now() < deadline) {
      const [error, response] = await sat20.receiveRGB11ProxyConsignment(requestID)
      if (!error) return response
      console.error(`[RGB11 Iris] proxy receive retry: ${error.message || String(error)}`)
      if (!/consignment is not available yet|witness is unresolved|outpoint status is unknown/i.test(
        String(error.message || error),
      )) {
        throw error
      }
      await new Promise((resolve) => setTimeout(resolve, 10_000))
    }
    throw new Error('Iris consignment was not available within 10 minutes')
  }

  await walletStorage.initializeState()
  await walletStorage.setValue('env', 'prd')
  await walletStorage.setValue('network', 'testnet')
  await walletStorage.setValue('chain', 'btc')
  await wallet.syncWalletCatalog().catch(() => [])
  const hashed = await verify.hashPassword(password)
  let sender = senderWalletId
    ? wallet.wallets.find((item) => item.id === senderWalletId)
    : wallet.wallets.find((item) => item.accounts.some((account) => account.address === senderAddress))
  if (!sender) {
    const [importError] = await wallet.importWallet(senderMnemonic, hashed)
    if (importError) throw importError
    const importedWalletID = wallet.walletId
    await wallet.syncWalletCatalog()
    sender = wallet.wallets.find((item) => item.id === importedWalletID)
  }
  if (!sender) throw new Error('RGB11 Iris test wallet is not imported')
  await wallet.setPassword(hashed)
  await wallet.setNetwork(Network.TESTNET)
  await wallet.setChain(Chain.BTC)
  await unwrap(await wallet.unlockWallet(hashed), 'unlockWallet')

  const other = wallet.wallets.find((item) => item.id !== sender.id)
  if (other) await wallet.switchWallet(other.id)
  await wallet.switchWallet(sender.id)
  await wallet.switchToAccount(0)
  await wallet.setChain(Chain.BTC)
  await unwrap(await sat20.switchAccount(0), 'switchAccount')
  if (!['iris-issued-invoice', 'receive-iris-issued'].includes(action)) {
    await waitForData()
  }

  if (action === 'fund-btc') {
    if (!irisBtcAddress.trim()) throw new Error('SAT20_IRIS_BTC_ADDRESS is required for fund-btc')
    const funded = await unwrap(
      await sat20.sendAssets(irisBtcAddress, '::', irisBtcAmount, '1'),
      'sendAssets for Iris Bitcoin funding',
    )
    return { action, address: irisBtcAddress, amount: irisBtcAmount, funded }
  }

  if (action === 'issue-file') {
    const checkpoint = readCheckpoint()
    const ticker = `F${Date.now().toString(36).slice(-7)}`.toUpperCase()
    const issuedResponse = await unwrap(await sat20.issueRGB11Asset({
      schema: 'NIA',
      ticker,
      name: `SAT20 file ${ticker}`,
      precision: 0,
      amounts: ['1'],
      min_confirmations: 1,
    }), 'issueRGB11Asset')
    const issued = JSON.parse(issuedResponse.result)
    const file = atob(issued.contract_consignment_base64 || '')
    if (!file.startsWith('RGB\u0000CON')) {
      throw new Error('RGB11 contract export is not a standard RGB contract file')
    }
    const updated = {
      ...checkpoint,
      fileContractTicker: ticker,
      fileContractId: issued.contract_id,
      fileContractConsignmentBase64: issued.contract_consignment_base64,
    }
    writeCheckpoint(updated)
    return { action, magic: 'RGB\\0CON', contractId: issued.contract_id, state: await parseState() }
  }

  if (action === 'send') {
    if (!irisInvoice.trim()) throw new Error('SAT20_IRIS_INVOICE is required for send')
    const ticker = `I${Date.now().toString(36).slice(-7)}`.toUpperCase()
    const issuedResponse = await unwrap(await sat20.issueRGB11Asset({
      schema: 'NIA',
      ticker,
      name: `SAT20 Iris ${ticker}`,
      precision: 0,
      amounts: [issueAmount],
      min_confirmations: 1,
    }), 'issueRGB11Asset')
    const issued = JSON.parse(issuedResponse.result)
    const preparedResponse = await unwrap(await sat20.prepareRGB11Transfer({
      invoice: irisInvoice,
      contract_id: issued.contract_id,
      amount_raw: sendAmount,
      fee_rate: 1,
      min_confirmations: 1,
    }), 'prepareRGB11Transfer')
    const prepared = JSON.parse(preparedResponse.transfer)
    if (prepared.state?.transport_mode !== 'rgb-json-rpc' || !prepared.state?.transfer_id) {
      throw new Error(`unexpected prepared transfer ${JSON.stringify(prepared.state)}`)
    }
    const checkpoint = {
      version: 1,
      ticker,
      assetName: `${issued.asset_name.Protocol}:${issued.asset_name.Type}:${issued.asset_name.Ticker}`,
      contractId: issued.contract_id,
      schemaId: issued.schema_id,
      contractArmor: issued.armor,
      contractConsignmentBase64: issued.contract_consignment_base64,
      irisInvoice,
      sendAmount,
      issueAmount,
      transferId: prepared.state.transfer_id,
    }
    writeCheckpoint(checkpoint)
    const broadcast = await unwrap(
      await sat20.deliverAndBroadcastRGB11ProxyTransfer([prepared.state.transfer_id]),
      'deliverAndBroadcastRGB11ProxyTransfer',
    )
    const completed = { ...checkpoint, txid: broadcast.txid }
    writeCheckpoint(completed)
    return { action, checkpoint: completed, state: await parseState() }
  }

  if (action === 'send-existing') {
    if (!irisInvoice.trim()) throw new Error('SAT20_IRIS_INVOICE is required for send-existing')
    if (!existingAssetName.trim()) throw new Error('SAT20_IRIS_ASSET_NAME is required for send-existing')
    const state = await parseState()
    const info = (state.ticker_infos || []).find((item) => (
      item.canonical_name === existingAssetName || assetName(item.name) === existingAssetName
    ))
    if (!info?.contract_id) throw new Error(`RGB11 asset is not registered: ${existingAssetName}`)
    const available = assetAmount(state.available_assets, existingAssetName)
    if (BigInt(available) < BigInt(sendAmount)) {
      throw new Error(`RGB11 asset ${existingAssetName} available ${available}, requested ${sendAmount}`)
    }
    const preparedResponse = await unwrap(await sat20.prepareRGB11Transfer({
      invoice: irisInvoice,
      contract_id: info.contract_id,
      amount_raw: sendAmount,
      fee_rate: 1,
      min_confirmations: 1,
    }), 'prepareRGB11Transfer')
    const prepared = JSON.parse(preparedResponse.transfer)
    if (prepared.state?.transport_mode !== 'rgb-json-rpc' || !prepared.state?.transfer_id) {
      throw new Error(`unexpected prepared transfer ${JSON.stringify(prepared.state)}`)
    }
    const checkpoint = {
      ...readCheckpoint(),
      version: 1,
      assetName: existingAssetName,
      ticker: info.name?.Ticker || existingAssetName.split(':').at(-1),
      contractId: info.contract_id,
      schemaId: info.schema_id || '',
      irisInvoice,
      sendAmount,
      transferId: prepared.state.transfer_id,
    }
    writeCheckpoint(checkpoint)
    const broadcast = await unwrap(
      await sat20.deliverAndBroadcastRGB11ProxyTransfer([prepared.state.transfer_id]),
      'deliverAndBroadcastRGB11ProxyTransfer',
    )
    const completed = { ...checkpoint, txid: broadcast.txid }
    writeCheckpoint(completed)
    return { action, checkpoint: completed, state: await parseState() }
  }

  const checkpoint = readCheckpoint()
  if (action === 'generic-invoice') {
    const invoice = await unwrap(await sat20.createRGB11Invoice({
      mode: 'witness',
      transport_mode: 'rgb-json-rpc',
      transport_endpoints: [irisProxyEndpoint],
      contract_id: '',
      schema_id: '',
      amount_raw: returnAmount,
      assignment_name: 'assetOwner',
      expiry: Math.floor(Date.now() / 1000) + 24 * 60 * 60,
      witness_vout: 1,
    }), 'createRGB11Invoice')
    const updated = {
      ...checkpoint,
      irisIssuedAmount: returnAmount,
      irisIssuedPwaInvoice: invoice.invoice,
      irisIssuedPwaRequestId: invoice.request_id || invoice.requestId,
    }
    writeCheckpoint(updated)
    return { action, invoice: invoice.invoice, requestId: invoice.request_id || invoice.requestId }
  }
  if (action === 'import-external-file') {
    if (!contractFileBase64) throw new Error('SAT20_RGB11_CONTRACT_FILE is required')
    const file = atob(contractFileBase64)
    if (!file.startsWith('RGB\u0000CON')) {
      throw new Error('external contract is not a standard RGB contract file')
    }
    const importedResponse = await unwrap(
      await sat20.importRGB11ContractFile(contractFileBase64),
      'importRGB11ContractFile',
    )
    const imported = JSON.parse(importedResponse.result)
    const updated = {
      ...readCheckpoint(),
      irisIssuedContractId: imported.contract_id,
      irisIssuedSchemaId: imported.schema_id,
      irisIssuedContractFileBase64: contractFileBase64,
    }
    writeCheckpoint(updated)
    return { action, magic: 'RGB\\0CON', imported, state: await parseState() }
  }
  if (action === 'iris-issued-invoice') {
    const checkpoint = readCheckpoint()
    if (!checkpoint.irisIssuedContractId || !checkpoint.irisIssuedSchemaId) {
      throw new Error('Iris-issued contract has not been imported')
    }
    const invoice = await unwrap(await sat20.createRGB11Invoice({
      mode: 'blind',
      transport_mode: 'rgb-json-rpc',
      transport_endpoints: [irisProxyEndpoint],
      contract_id: checkpoint.irisIssuedContractId,
      schema_id: checkpoint.irisIssuedSchemaId,
      amount_raw: returnAmount,
      assignment_name: 'assetOwner',
      expiry: Math.floor(Date.now() / 1000) + 24 * 60 * 60,
      witness_vout: 1,
    }), 'createRGB11Invoice')
    const updated = {
      ...checkpoint,
      irisIssuedAmount: returnAmount,
      irisIssuedPwaInvoice: invoice.invoice,
      irisIssuedPwaRequestId: invoice.request_id || invoice.requestId,
    }
    writeCheckpoint(updated)
    return { action, invoice: invoice.invoice, requestId: invoice.request_id || invoice.requestId }
  }
  if (action === 'receive-iris-issued') {
    if (!checkpoint.irisIssuedPwaRequestId) {
      throw new Error('Iris-issued PWA invoice has not been created')
    }
    const received = await retryProxyReceive(checkpoint.irisIssuedPwaRequestId)
    const updated = { ...checkpoint, irisIssuedReceive: received }
    writeCheckpoint(updated)
    return { action, received }
  }
  if (!checkpoint.contractId) throw new Error('Iris test checkpoint is missing')
  if (action === 'import-file') {
    const contractFile = checkpoint.fileContractConsignmentBase64 || checkpoint.contractConsignmentBase64
    const expectedContractId = checkpoint.fileContractId || checkpoint.contractId
    if (!contractFile) {
      throw new Error('RGB11 standard contract file is missing')
    }
    const file = atob(contractFile)
    if (!file.startsWith('RGB\u0000CON')) {
      throw new Error('RGB11 contract export is not a standard RGB contract file')
    }
    const importedResponse = await unwrap(
      await sat20.importRGB11ContractFile(contractFile),
      'importRGB11ContractFile',
    )
    const imported = JSON.parse(importedResponse.result)
    if (imported.contract_id !== expectedContractId) {
      throw new Error(`imported contract ${imported.contract_id} does not match ${expectedContractId}`)
    }
    return { action, magic: 'RGB\\0CON', imported, state: await parseState() }
  }
  if (action === 'deliver') {
    if (!checkpoint.transferId) throw new Error('Iris transfer has not been prepared')
    const broadcast = await unwrap(
      await sat20.deliverAndBroadcastRGB11ProxyTransfer([checkpoint.transferId]),
      'deliverAndBroadcastRGB11ProxyTransfer',
    )
    const updated = { ...checkpoint, txid: broadcast.txid }
    writeCheckpoint(updated)
    return { action, checkpoint: updated, state: await parseState() }
  }
  if (action === 'ack') {
    if (!checkpoint.transferId) throw new Error('Iris transfer has not been prepared')
    const ack = await unwrap(
      await sat20.fetchRGB11ProxyAck(checkpoint.transferId),
      'fetchRGB11ProxyAck',
    )
    const updated = { ...checkpoint, irisAck: ack }
    writeCheckpoint(updated)
    return { action, checkpoint: updated, state: await parseState() }
  }
  if (action === 'invoice') {
    const invoice = await unwrap(await sat20.createRGB11Invoice({
      mode: returnInvoiceMode,
      transport_mode: 'rgb-json-rpc',
      transport_endpoints: [irisProxyEndpoint],
      contract_id: checkpoint.contractId,
      schema_id: checkpoint.schemaId,
      amount_raw: returnAmount,
      assignment_name: 'assetOwner',
      expiry: Math.floor(Date.now() / 1000) + 24 * 60 * 60,
      ...(returnInvoiceMode === 'witness' ? { witness_vout: 1 } : {}),
    }), 'createRGB11Invoice')
    const updated = {
      ...checkpoint,
      returnAmount,
      pwaInvoice: invoice.invoice,
      pwaRequestId: invoice.request_id || invoice.requestId,
    }
    writeCheckpoint(updated)
    return { action, checkpoint: updated, state: await parseState() }
  }
  if (action === 'receive') {
    if (!checkpoint.pwaRequestId) throw new Error('PWA return invoice has not been created')
    const received = await retryProxyReceive(checkpoint.pwaRequestId)
    const updated = { ...checkpoint, returnReceive: received }
    writeCheckpoint(updated)
    return { action, checkpoint: updated, state: await parseState() }
  }
  return { action: 'status', checkpoint, state: await parseState() }
}, {
  action: ACTION,
  password: PASSWORD,
  senderMnemonic: SENDER_MNEMONIC,
  senderAddress: SENDER_ADDRESS,
  senderWalletId: SENDER_WALLET_ID,
  irisInvoice: IRIS_INVOICE,
  existingAssetName: EXISTING_ASSET_NAME,
  issueAmount: ISSUE_AMOUNT,
  sendAmount: SEND_AMOUNT,
  returnAmount: RETURN_AMOUNT,
  returnInvoiceMode: RETURN_INVOICE_MODE,
  irisBtcAddress: IRIS_BTC_ADDRESS,
  irisBtcAmount: IRIS_BTC_AMOUNT,
  irisProxyEndpoint: IRIS_PROXY_ENDPOINT,
  contractFileBase64: CONTRACT_FILE_BASE64,
  checkpointKey: CHECKPOINT_KEY,
})

console.log(JSON.stringify(result, null, 2))
await new Promise((resolve) => setTimeout(resolve, 100))
process.exit(0)
