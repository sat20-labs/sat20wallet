<template>
  <Dialog :open="isOpen" @update:open="isOpen = $event">
    <DialogContent class="max-h-[90vh] w-[360px] overflow-y-auto rounded-lg bg-black">
      <DialogHeader>
        <DialogTitle>{{ $t('rgb11Invoice.title') }}</DialogTitle>
        <DialogDescription class="break-all font-mono text-xs">
          {{ assetContractID }}
        </DialogDescription>
      </DialogHeader>

      <div class="space-y-4">
        <div class="space-y-2">
          <Label>{{ $t('rgb11Invoice.transport') }}</Label>
          <select v-model="transportMode" class="h-10 w-full rounded-md border border-zinc-700 bg-zinc-900 px-3 text-sm"
            :disabled="loading || !!requestId">
            <option value="rgb-json-rpc">{{ $t('rgb11Invoice.standardTransport') }}</option>
            <option value="out-of-band">{{ $t('rgb11Invoice.outOfBandTransport') }}</option>
          </select>
          <p class="text-xs text-zinc-500">{{ $t(`rgb11Invoice.${transportMode === 'rgb-json-rpc' ? 'standardTransportHelp' : 'outOfBandTransportHelp'}`) }}</p>
        </div>

        <div v-if="transportMode === 'rgb-json-rpc'" class="space-y-2">
          <Label>{{ $t('rgb11Invoice.proxyEndpoint') }}</Label>
          <Input v-model="proxyEndpoint" inputmode="url" :placeholder="$t('rgb11Invoice.proxyEndpointPlaceholder')"
            class="h-12 bg-zinc-800 font-mono text-xs" :disabled="loading || !!requestId" />
          <p class="text-xs text-zinc-500">{{ $t('rgb11Invoice.proxyEndpointHelp') }}</p>
        </div>

        <div class="space-y-2">
          <Label>{{ $t('rgb11Invoice.amount') }}</Label>
          <Input v-model="amount" inputmode="decimal" :placeholder="$t('rgb11Invoice.enterAmount')"
            class="h-12 bg-zinc-800" :disabled="loading || !!requestId" />
          <p v-if="errorMessage" class="text-xs text-red-400">{{ errorMessage }}</p>
        </div>

        <div class="space-y-2">
          <Label>{{ $t('rgb11Invoice.receiveMode') }}</Label>
          <select v-model="receiveMode" class="h-10 w-full rounded-md border border-zinc-700 bg-zinc-900 px-3 text-sm"
            :disabled="loading || !!requestId">
            <option value="witness">{{ $t('rgb11Invoice.witnessMode') }}</option>
            <option value="blind">{{ $t('rgb11Invoice.blindMode') }}</option>
          </select>
          <p class="text-xs text-zinc-500">{{ $t(`rgb11Invoice.${receiveMode}ModeHelp`) }}</p>
        </div>

        <p class="text-xs text-zinc-500">{{ $t('rgb11Invoice.expires') }}</p>

        <div v-if="invoice" class="space-y-2">
          <Label>{{ $t('rgb11Invoice.invoice') }}</Label>
          <p class="break-all font-mono text-xs">{{ requestId }}</p>
          <Textarea :model-value="invoice" readonly spellcheck="false"
            class="min-h-32 resize-none bg-zinc-900 font-mono text-xs" />
          <Button variant="outline" class="w-full" @click="copyInvoice">
            <Icon icon="lucide:copy" class="mr-2 h-4 w-4" />
            {{ $t('rgb11Invoice.copy') }}
          </Button>
        </div>

        <div v-if="requestId && transportMode === 'rgb-json-rpc'" class="space-y-2 border-t border-zinc-800 pt-3">
          <Button variant="outline" class="w-full" :disabled="loading" @click="checkProxyTransfer">
            {{ loading ? $t('rgb11Invoice.checkingTransfer') : $t('rgb11Invoice.checkTransfer') }}
          </Button>
        </div>

        <div v-if="requestId && transportMode === 'out-of-band'" class="space-y-2 border-t border-zinc-800 pt-3">
          <Label>{{ $t('rgb11Transfer.package') }}</Label>
          <Textarea v-model="transferPackage" :disabled="loading" spellcheck="false"
            class="min-h-32 bg-zinc-900 font-mono text-xs" />
          <Label for="rgb11-consignment-file">{{ $t('rgb11Transfer.selectArmoredFile') }}</Label>
          <input id="rgb11-consignment-file" type="file" accept=".asc,.txt,.json" :disabled="loading" @change="selectPackageFile" />
          <p v-if="packageHash && !transferPackage" class="text-xs text-amber-400">{{ $t('rgb11Transfer.reselectOriginal') }}</p>
          <Button variant="outline" class="w-full" :disabled="loading || expired || !!sdkTerminalStatus || !transferPackage.trim() || phase !== 'created'" @click="preparePackage">
            {{ $t('rgb11Transfer.prepareReceive') }}
          </Button>
          <div v-if="summary" class="space-y-2">
            <p class="text-xs text-amber-400">{{ $t('rgb11Transfer.prebroadcastOnly') }}</p>
            <Textarea :model-value="JSON.stringify(summary, null, 2)" readonly class="min-h-32 bg-zinc-900 font-mono text-xs" />
            <Button variant="outline" class="w-full" @click="copy(JSON.stringify(summary))">{{ $t('rgb11Transfer.copySummary') }}</Button>
            <Button class="w-full" :disabled="loading || expired || !!sdkTerminalStatus || !transferPackage.trim() || phase === 'accepted'" @click="acceptPackage">
              {{ $t('rgb11Transfer.acceptAfterBroadcast') }}
            </Button>
          </div>
          <p v-if="expired && phase !== 'accepted'" class="text-xs text-amber-400">{{ $t('rgb11Transfer.requestExpired') }}</p>
          <p v-if="phase === 'accepted'" class="text-xs text-emerald-400">{{ $t('rgb11Transfer.acceptedOutOfBand') }}</p>
        </div>
        <div v-if="requestId" class="space-y-2 border-t border-zinc-800 pt-3">
          <Button variant="outline" class="w-full" :disabled="loading" @click="checkRequestState">{{ $t('rgb11Transfer.checkRequestState') }}</Button>
          <p v-if="sdkTerminalStatus" class="text-xs text-zinc-400">{{ $t('rgb11Transfer.sdkRequestState', { status: sdkTerminalStatus }) }}</p>
          <template v-if="canStartNewRequest">
            <p class="text-xs text-amber-400">{{ $t('rgb11Transfer.newRequestLocalOnly') }}</p>
            <Button variant="outline" class="w-full" :disabled="loading || restoring" @click="startNewRequest">{{ $t('rgb11Transfer.startNewRequest') }}</Button>
          </template>
        </div>
      </div>

      <DialogFooter>
        <Button class="w-full" :disabled="loading || restoring || restoreFailed || !scopeKey || !amount.trim() || (!!requestId)" @click="generateInvoice">
          {{ loading ? $t('rgb11Invoice.generating') : $t('rgb11Invoice.generate') }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Icon } from '@iconify/vue'
import { useI18n } from 'vue-i18n'
import { useClipboard, useNow } from '@vueuse/core'
import walletManager from '@/utils/sat20'
import { useToast } from '@/components/ui/toast-new'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Storage } from '@/lib/storage-adapter'
import { sha256Text, rgb11Amount, rgb11ReceivePackages, rgb11ResumeConsignment, type RGB11PrebroadcastSummary } from '@/utils/rgb11Oob'
import { useGlobalStore } from '@/store/global'
import { useWalletStore } from '@/store/wallet'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

interface RGB11Asset {
  ticker: string
  contract_id?: string
  label?: string
  precision?: number
}

const props = defineProps<{ asset: RGB11Asset | null }>()
const isOpen = defineModel('open', { type: Boolean })
const amount = ref('')
const receiveMode = ref<'blind' | 'witness'>('witness')
const transportMode = ref<'out-of-band' | 'rgb-json-rpc'>('out-of-band')
const proxyEndpoint = ref('')
const invoice = ref('')
const requestId = ref('')
const transferPackage = ref('')
const errorMessage = ref('')
const loading = ref(false)
const restoring = ref(false)
const restoreFailed = ref(false)
const phase = ref<'created' | 'prepared' | 'accepted'>('created')
const summary = ref<RGB11PrebroadcastSummary | null>(null)
const packageHash = ref('')
const expiresAt = ref(0)
const sdkTerminalStatus = ref('')
const now = useNow({ interval: 60000 })
const expired = computed(() => !!expiresAt.value && expiresAt.value <= Math.floor(now.value.getTime() / 1000))
const canStartNewRequest = computed(() => !!requestId.value && (expired.value || phase.value === 'accepted' || !!sdkTerminalStatus.value))
const { t } = useI18n()
const { toast } = useToast()
const { copy } = useClipboard()
const globalStore = useGlobalStore()
const walletStore = useWalletStore()
const assetContractID = computed(() => {
  const value = props.asset?.contract_id?.trim() || ''
  return !value || value.startsWith('rgb:') ? value : `rgb:${value}`
})

// Persist only the receive handle and minimal matching metadata. Never persist
// consignment contents, receipt allocations, seal disclosures or blinding data.
const scopeKey = computed(() => {
  if (!walletStore.rootAccountId || !walletStore.walletId || walletStore.accountIndex == null || walletStore.locked ||
      walletStore.isSwitchingWallet || walletStore.isSwitchingAccount || walletStore.isSwitchingNetwork) return ''
  return `rgb11:receive:v1:${JSON.stringify([globalStore.env, walletStore.network, walletStore.rootAccountId,
    walletStore.walletId, walletStore.accountIndex, assetContractID.value])}`
})
const requestMetadata = () => ({ version: 1, requestId: requestId.value, invoice: invoice.value,
  amount: amount.value, receiveMode: receiveMode.value, transportMode: transportMode.value,
  phase: phase.value, summary: summary.value, packageHash: packageHash.value, expiresAt: expiresAt.value,
  sdkTerminalStatus: sdkTerminalStatus.value })
const persistRequest = async (key = scopeKey.value) => {
  if (!key || !requestId.value) return
  await Storage.set({ key, value: JSON.stringify(requestMetadata()) })
}
const ensureScope = (key: string) => {
  if (!key || key !== scopeKey.value) throw new Error(t('rgb11Transfer.scopeChanged'))
}

// A missing SDK transfer is not evidence of cancellation. Only the unique
// receive state for this exact invoice can establish a known SDK terminal state.
const checkRequestState = async () => {
  const key = scopeKey.value
  const id = requestId.value
  if (!key || !id || loading.value) return
  loading.value = true
  errorMessage.value = ''
  try {
    const [err, result] = await walletManager.getRGB11State()
    if (err || !result?.state) throw err || new Error(t('rgb11Invoice.receiveFailed'))
    ensureScope(key)
    if (id !== requestId.value) return
    const matches = (JSON.parse(result.state).transfers || []).filter((state: any) =>
      state.direction === 'receive' && state.invoice === invoice.value)
    if (matches.length !== 1) return
    const status = matches[0].status
    if (status === 'pending' || status === 'settled') {
      phase.value = 'accepted' // SDK accept completed; pending still is not spendable.
      sdkTerminalStatus.value = status
    } else if (status === 'rejected') {
      sdkTerminalStatus.value = status
    } else {
      sdkTerminalStatus.value = ''
    }
    await persistRequest(key)
  } catch (error: any) { errorMessage.value = error?.message || t('rgb11Invoice.receiveFailed') }
  finally { loading.value = false }
}

const startNewRequest = async () => {
  const key = scopeKey.value
  if (!key || loading.value || !canStartNewRequest.value) return
  loading.value = true
  errorMessage.value = ''
  try {
    const metadata = requestMetadata()
    // Archive first: failures leave the active request intact. This changes
    // only the PWA selection, never SDK cancellation, UTXO locks or chain state.
    await Storage.set({ key: `${key}:history:${encodeURIComponent(metadata.requestId)}`,
      value: JSON.stringify({ ...metadata, archivedAt: Date.now(), localReason: expired.value ? 'invoice-expired' : 'receive-completed' }) })
    ensureScope(key)
    await Storage.remove({ key })
    ensureScope(key)
    rgb11ReceivePackages.delete(key)
    requestId.value = ''
    invoice.value = ''
    transferPackage.value = ''
    amount.value = ''
    phase.value = 'created'
    summary.value = null
    packageHash.value = ''
    expiresAt.value = 0
    sdkTerminalStatus.value = ''
  } catch (error: any) { errorMessage.value = error?.message || t('rgb11Transfer.persistenceFailed') }
  finally { loading.value = false }
}

const maxUint64 = '18446744073709551615'
const proxyStorageKey = () => `rgb11:proxy:${globalStore.env}:${walletStore.network || 'mainnet'}`

const loadProxyEndpoint = async () => {
  const { value } = await Storage.get({ key: proxyStorageKey() })
  proxyEndpoint.value = value || ''
}

const decimalToRaw = (input: string, precision: number): string | null => {
  const text = input.trim()
  const match = /^(0|[1-9]\d*)(?:\.(\d+))?$/.exec(text)
  if (!match) return null
  const fraction = match[2] || ''
  if (fraction.length > precision) return null
  const raw = `${match[1]}${fraction.padEnd(precision, '0')}`.replace(/^0+(?=\d)/, '')
  if (raw === '0' || raw.length > maxUint64.length || (raw.length === maxUint64.length && raw > maxUint64)) {
    return null
  }
  return raw
}

const generateInvoice = async () => {
  if (restoring.value || restoreFailed.value || !scopeKey.value || requestId.value) return
  if (!assetContractID.value) {
    errorMessage.value = t('rgb11Invoice.contractIdMissing')
    return
  }
  const key = scopeKey.value
  const precision = Math.max(0, Number(props.asset?.precision || 0))
  const amountRaw = decimalToRaw(amount.value, precision)
  if (!amountRaw || !props.asset?.ticker) {
    errorMessage.value = t('rgb11Invoice.invalidAmount', { precision })
    return
  }
  const endpoint = proxyEndpoint.value.trim()
  if (transportMode.value === 'rgb-json-rpc' && !endpoint) {
    errorMessage.value = t('rgb11Invoice.proxyEndpointRequired')
    return
  }

  loading.value = true
  errorMessage.value = ''
  if (transportMode.value === 'rgb-json-rpc') {
    await Storage.set({ key: proxyStorageKey(), value: endpoint })
  }
  const expiry = Math.floor(Date.now() / 1000) + 24 * 60 * 60
  const draft = { amount: amount.value, receiveMode: receiveMode.value, transportMode: transportMode.value, expiresAt: expiry }
  const [err, result] = await walletManager.createRGB11Invoice({
    mode: receiveMode.value,
    transport_mode: transportMode.value,
    ...(transportMode.value === 'rgb-json-rpc' ? { transport_endpoints: [endpoint] } : {}),
    contract_id: assetContractID.value,
    amount_raw: amountRaw,
    assignment_name: 'assetOwner',
    expiry,
    witness_vout: 1,
  })
  loading.value = false
  if (err || !result?.invoice) {
    errorMessage.value = err?.message || t('rgb11Invoice.generateFailed')
    return
  }
  const savedRequest = { version: 1, ...draft, requestId: result.request_id || result.requestId || '',
    invoice: result.invoice, phase: 'created', summary: null, packageHash: '' }
  try { await Storage.set({ key, value: JSON.stringify(savedRequest) }) }
  catch { if (key === scopeKey.value) errorMessage.value = t('rgb11Transfer.persistenceFailed') }
  if (key !== scopeKey.value) return
  phase.value = 'created'
  sdkTerminalStatus.value = ''
  expiresAt.value = expiry
  summary.value = null
  packageHash.value = ''
  transferPackage.value = ''
  rgb11ReceivePackages.delete(key)
  invoice.value = result.invoice
  requestId.value = result.request_id || result.requestId || ''

}

const checkProxyTransfer = async () => {
  const key = scopeKey.value
  loading.value = true
  errorMessage.value = ''
  try {
    const [err, result] = await walletManager.receiveRGB11ProxyConsignment(requestId.value)
    if (err || !result?.ack_posted) throw err || new Error(t('rgb11Invoice.receiveFailed'))
    ensureScope(key)
    phase.value = 'accepted'
    await persistRequest(key)
    await refreshRGB11State()
    toast({
      title: t('rgb11Invoice.received', { txid: result.txid }),
      variant: 'success',
      duration: 3000,
    })
  } catch (error: any) {
    errorMessage.value = error?.message || t('rgb11Invoice.noTransfer')
  } finally {
    loading.value = false
  }
}

const copyInvoice = async () => {
  if (!invoice.value) return
  await copy(invoice.value)
  toast({ title: t('rgb11Invoice.copied'), variant: 'success', duration: 1500 })
}

const refreshRGB11State = async () => {
  const [err] = await walletManager.refreshRGB11State()
  if (err) throw err
}

const packageInput = async () => {
  let consignment: string
  try {
    consignment = await rgb11ResumeConsignment(transferPackage.value, packageHash.value,
      summary.value?.consignment_hash || '', phase.value === 'prepared')
  } catch { throw new Error(t('rgb11Transfer.wrongOriginal')) }
  const hash = await sha256Text(consignment)
  return { consignment, hash }
}

const selectPackageFile = async (event: Event) => {
  const file = (event.target as HTMLInputElement).files?.[0]
  if (!file) return
  const key = scopeKey.value
  const text = await file.text()
  if (key === scopeKey.value) transferPackage.value = text
}

const preparePackage = async () => {
  if (phase.value !== 'created') return
  const key = scopeKey.value
  loading.value = true
  errorMessage.value = ''
  try {
    ensureScope(key)
    const { consignment, hash } = await packageInput()
    ensureScope(key)
    const [err, result] = await walletManager.prepareRGB11Consignment(requestId.value, consignment)
    if (err || !result?.receipt) throw err || new Error(t('rgb11Transfer.acceptFailed'))
    ensureScope(key)
    const receipt = JSON.parse(result.receipt)
    const [stateErr, resultState] = await walletManager.getRGB11State()
    if (stateErr || !resultState?.state) throw stateErr || new Error(t('rgb11Transfer.acceptFailed'))
    ensureScope(key)
    const states = JSON.parse(resultState.state).transfers || []
    const matches = states.filter((state: any) => state.direction === 'receive' && state.invoice === invoice.value &&
      state.consignment_hash === receipt.consignment_hash && state.transfer_id === receipt.transfer_id &&
      state.status === 'awaiting_broadcast' && state.transport_mode === 'out-of-band')
    if (matches.length !== 1 || receipt.contract_id !== assetContractID.value || !receipt.schema_id) throw new Error(t('rgb11Transfer.summaryMismatch'))
    const state = matches[0]
    const allocation = rgb11Amount(state)
    const expected = decimalToRaw(amount.value, Math.max(0, Number(props.asset?.precision || 0)))
    if (allocation.amount_raw !== expected || !state.witness_txid || state.output_outpoints?.length !== 1) throw new Error(t('rgb11Transfer.summaryMismatch'))
    const invoiceHash = await sha256Text(invoice.value)
    ensureScope(key)
    summary.value = { version: 1, stage: 'validated-awaiting-broadcast', contract_id: receipt.contract_id,
      schema_id: receipt.schema_id, consignment_hash: receipt.consignment_hash, transfer_id: receipt.transfer_id,
      witness_txid: state.witness_txid, invoice_hash: invoiceHash, ...allocation, recipient_outpoint: state.output_outpoints[0] }
    packageHash.value = hash
    phase.value = 'prepared'
    await persistRequest(key)
  } catch (error: any) {
    errorMessage.value = error?.message || t('rgb11Transfer.acceptFailed')
  } finally { loading.value = false }
}

const acceptPackage = async () => {
  const key = scopeKey.value
  if (phase.value !== 'prepared' || !summary.value) return
  loading.value = true
  errorMessage.value = ''
  try {
    ensureScope(key)
    const { consignment } = await packageInput()
    ensureScope(key)
    const [err, result] = await walletManager.acceptRGB11Consignment(requestId.value, consignment)
    if (err || !result?.receipt) throw err || new Error(t('rgb11Transfer.acceptFailed'))
    ensureScope(key)
    phase.value = 'accepted'
    await persistRequest(key)
    await refreshRGB11State()
    toast({ title: t('rgb11Transfer.acceptedOutOfBand'), variant: 'success', duration: 2500 })
  } catch (error: any) {
    errorMessage.value = `${t('rgb11Transfer.acceptRetry')} ${error?.message || ''}`
  } finally { loading.value = false }
}

watch(scopeKey, async (key) => {
  // Closing the dialog does not alter scope or clear its in-memory consignment.
  invoice.value = ''
  requestId.value = ''
  transferPackage.value = ''
  amount.value = ''
  phase.value = 'created'
  summary.value = null
  packageHash.value = ''
  expiresAt.value = 0
  sdkTerminalStatus.value = ''
  errorMessage.value = ''
  restoring.value = false
  restoreFailed.value = false
  if (!key) return
  restoring.value = true
  try {
    const { value } = await Storage.get({ key })
    if (key !== scopeKey.value || !value) return
    const saved = JSON.parse(value)
    if (saved.version !== 1 || typeof saved.requestId !== 'string' || !saved.requestId || typeof saved.invoice !== 'string') throw new Error('Invalid saved receive request')
    requestId.value = saved.requestId
    invoice.value = saved.invoice
    amount.value = saved.amount || ''
    receiveMode.value = saved.receiveMode === 'blind' ? 'blind' : 'witness'
    transportMode.value = saved.transportMode === 'rgb-json-rpc' ? 'rgb-json-rpc' : 'out-of-band'
    phase.value = ['prepared', 'accepted'].includes(saved.phase) ? saved.phase : 'created'
    summary.value = saved.summary || null
    packageHash.value = saved.packageHash || ''
    expiresAt.value = Number(saved.expiresAt) || 0
    sdkTerminalStatus.value = ['pending', 'settled', 'rejected'].includes(saved.sdkTerminalStatus) ? saved.sdkTerminalStatus : ''
    transferPackage.value = rgb11ReceivePackages.get(key) || ''
  } catch { if (key === scopeKey.value) { restoreFailed.value = true; errorMessage.value = t('rgb11Transfer.persistenceFailed') } }
  finally { if (key === scopeKey.value) restoring.value = false }
}, { immediate: true })

watch(transferPackage, (value) => {
  if (scopeKey.value && value) rgb11ReceivePackages.set(scopeKey.value, value)
})

watch(isOpen, (open) => { if (open) void loadProxyEndpoint() })
</script>
