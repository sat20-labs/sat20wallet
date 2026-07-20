import { readFile, stat } from 'node:fs/promises'

const requiredFiles = [
  'public/wasm/sat20wallet.wasm',
  'components/wallet/RGB11IssueDialog.vue',
  'components/wallet/RGB11ImportDialog.vue',
  'components/wallet/RGB11InvoiceDialog.vue',
  'components/wallet/RGB11SendDialog.vue',
  'utils/rgb11Address.ts',
  'composables/hooks/useRgb11Assets.ts',
  'composables/hooks/useUnifiedAssets.ts',
  'store/rgb11.ts',
  'entrypoints/popup/pages/wallet/Tools.vue',
]

const requiredWasmMethods = [
  'getRGB11State',
  'issueRGB11Asset',
  'importRGB11Contract',
  'createRGB11Invoice',
  'prepareRGB11Transfer',
  'buildRGB11RelayRecord',
  'publishRGB11RelayRecord',
  'acceptRGB11RelayConsignment',
  'rejectRGB11RelayConsignment',
  'publishRGB11AckRecord',
  'fetchRGB11AckRecord',
  'cancelRGB11BatchByNack',
  'broadcastRGB11Transfer',
  'broadcastRGB11Batch',
  'broadcastRGB11OutOfBand',
  'refreshRGB11State',
  'backupRGB11WalletState',
  'restoreRGB11WalletState',
  'getRGB11AddressCarrierWarning',
  'syncRGB11AddressMailbox',
  'deliverAndBroadcastRGB11AddressTransfer',
  'prepareRGB11AddressTransfer',
  'resolveRGB11AddressEndpoint',
  'enableRGB11AddressReceive',
]

const requireContains = async (path, fragments) => {
  const content = await readFile(path, 'utf8')
  for (const fragment of fragments) {
    if (!content.includes(fragment)) {
      throw new Error(`${path} is missing ${JSON.stringify(fragment)}`)
    }
  }
}

for (const path of requiredFiles) {
  const info = await stat(path)
  if (!info.isFile() || info.size === 0) throw new Error(`${path} is missing or empty`)
}

const wasm = await readFile('public/wasm/sat20wallet.wasm')
const wasmText = wasm.toString('latin1')
for (const method of requiredWasmMethods) {
  if (!wasmText.includes(method)) throw new Error(`wallet WASM is missing RGB11 export ${method}`)
}

await requireContains('utils/sat20.ts', requiredWasmMethods.filter((method) => ![
'enableRGB11AddressReceive',
'resolveRGB11AddressEndpoint',
'prepareRGB11AddressTransfer',
'deliverAndBroadcastRGB11AddressTransfer',
'syncRGB11AddressMailbox',
'getRGB11AddressCarrierWarning',
].includes(method)))
await requireContains('utils/rgb11Address.ts', [
'enableRGB11AddressReceive',
'resolveRGB11AddressEndpoint',
'prepareRGB11AddressTransfer',
'deliverAndBroadcastRGB11AddressTransfer',
'syncRGB11AddressMailbox',
'getRGB11AddressCarrierWarning',
])
await requireContains('components/asset/L1AssetsTabs.vue', [
  "selectedType === 'RGB11'",
  "asset.protocol !== 'rgb11'",
  "asset.protocol === 'rgb11'",
  'backupRGB11State',
  'restoreRGB11State',
  'rgb11Transfers',
  'rgb11TransferStatusClass',
])
await requireContains('components/wallet/RGB11InvoiceDialog.vue', [
  'decimalToRaw',
  "mode: receiveMode.value",
  '<option value="witness">',
  '<option value="blind">',
  'acceptRGB11RelayConsignment',
  'rejectRGB11RelayConsignment',
  'publishRGB11AckRecord',
])
await requireContains('components/wallet/RGB11SendDialog.vue', [
  'prepareRGB11Transfer',
  'publishRGB11RelayRecord',
  'fetchRGB11AckRecord',
	'cancelRGB11BatchByNack',
  'broadcastRGB11Transfer',
	'broadcastRGB11Batch',
	'broadcastRGB11OutOfBand',
	'invoices',
	'outOfBand.value ? null : JSON.parse',
  'rgb11Address.prepareTransfer',
  'rgb11Address.deliverAndBroadcast',
])
await requireContains('composables/hooks/useRgb11Assets.ts', [
  'tickerInfoFor',
  'officialContractID',
  'display_name',
  'rgb11Address.syncMailbox',
])
await requireContains('entrypoints/popup/pages/wallet/Tools.vue', [
  'tools.rgb11.deployTitle',
  'showRGB11Issue = true',
  '<RGB11IssueDialog',
])
await requireContains('components/wallet/RGB11ImportDialog.vue', [
  'importRGB11Contract',
  "t('rgb11Transfer.imported'",
])
await requireContains('components/wallet/RGB11IssueDialog.vue', [
  'issueRGB11Asset',
  '<option value="NIA">NIA</option>',
  '<option value="IFA">IFA</option>',
  '<option value="UDA">UDA</option>',
  'inflation_amounts',
  'validTicker',
  'copyContract',
  "t('rgb11Transfer.issued'",
])
for (const dialog of [
  'components/wallet/RGB11InvoiceDialog.vue',
  'components/wallet/RGB11SendDialog.vue',
  'utils/rgb11Address.ts',
  'components/wallet/RGB11ImportDialog.vue',
  'components/wallet/RGB11IssueDialog.vue',
]) {
  const content = await readFile(dialog, 'utf8')
  if (content.includes('backupRGB11WalletState')) {
    throw new Error(`${dialog} must rely on enrolled automatic backup instead of writing a paid backup after every action`)
  }
}
const issueDialog = await readFile('components/wallet/RGB11IssueDialog.vue', 'utf8')
if (issueDialog.includes('<option value="CFA">')) {
  throw new Error('CFA must not be exposed by first-release RGB11 issuance')
}

console.log(JSON.stringify({
  status: 'ok',
  wasmBytes: wasm.byteLength,
  wasmExports: requiredWasmMethods,
  checkedFiles: requiredFiles,
}, null, 2))
