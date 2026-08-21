<template>
  <Dialog :open="isOpen" @update:open="isOpen = $event">
    <DialogContent class="w-[360px] rounded-lg bg-black">
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
            :disabled="loading">
            <option value="rgb-json-rpc">{{ $t('rgb11Invoice.standardTransport') }}</option>
            <option value="out-of-band">{{ $t('rgb11Invoice.outOfBandTransport') }}</option>
          </select>
          <p class="text-xs text-zinc-500">{{ $t(`rgb11Invoice.${transportMode === 'rgb-json-rpc' ? 'standardTransportHelp' : 'outOfBandTransportHelp'}`) }}</p>
        </div>

        <div v-if="transportMode === 'rgb-json-rpc'" class="space-y-2">
          <Label>{{ $t('rgb11Invoice.proxyEndpoint') }}</Label>
          <Input v-model="proxyEndpoint" inputmode="url" :placeholder="$t('rgb11Invoice.proxyEndpointPlaceholder')"
            class="h-12 bg-zinc-800 font-mono text-xs" :disabled="loading" />
          <p class="text-xs text-zinc-500">{{ $t('rgb11Invoice.proxyEndpointHelp') }}</p>
        </div>

        <div class="space-y-2">
          <Label>{{ $t('rgb11Invoice.amount') }}</Label>
          <Input v-model="amount" inputmode="decimal" :placeholder="$t('rgb11Invoice.enterAmount')"
            class="h-12 bg-zinc-800" :disabled="loading" />
          <p v-if="errorMessage" class="text-xs text-red-400">{{ errorMessage }}</p>
        </div>

        <div class="space-y-2">
          <Label>{{ $t('rgb11Invoice.receiveMode') }}</Label>
          <select v-model="receiveMode" class="h-10 w-full rounded-md border border-zinc-700 bg-zinc-900 px-3 text-sm"
            :disabled="loading">
            <option value="witness">{{ $t('rgb11Invoice.witnessMode') }}</option>
            <option value="blind">{{ $t('rgb11Invoice.blindMode') }}</option>
          </select>
          <p class="text-xs text-zinc-500">{{ $t(`rgb11Invoice.${receiveMode}ModeHelp`) }}</p>
        </div>

        <p class="text-xs text-zinc-500">{{ $t('rgb11Invoice.expires') }}</p>

        <div v-if="invoice" class="space-y-2">
          <Label>{{ $t('rgb11Invoice.invoice') }}</Label>
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
          <Textarea v-model="transferPackage" spellcheck="false"
            class="min-h-32 bg-zinc-900 font-mono text-xs" />
          <Button variant="outline" class="w-full" :disabled="loading || !transferPackage.trim()" @click="acceptPackage">
            {{ loading ? $t('rgb11Transfer.accepting') : $t('rgb11Transfer.accept') }}
          </Button>
        </div>
      </div>

      <DialogFooter>
        <Button class="w-full" :disabled="loading || !amount.trim()" @click="generateInvoice">
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
import { useClipboard } from '@vueuse/core'
import walletManager from '@/utils/sat20'
import { useToast } from '@/components/ui/toast-new'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Storage } from '@/lib/storage-adapter'
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
const { t } = useI18n()
const { toast } = useToast()
const { copy } = useClipboard()
const globalStore = useGlobalStore()
const walletStore = useWalletStore()
const assetContractID = computed(() => {
  const value = props.asset?.contract_id || props.asset?.ticker || ''
  return value.startsWith('rgb:') ? value : `rgb:${value}`
})

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
  const [err, result] = await walletManager.createRGB11Invoice({
    mode: receiveMode.value,
    transport_mode: transportMode.value,
    ...(transportMode.value === 'rgb-json-rpc' ? { transport_endpoints: [endpoint] } : {}),
    contract_id: assetContractID.value,
    amount_raw: amountRaw,
    assignment_name: 'assetOwner',
    expiry: Math.floor(Date.now() / 1000) + 24 * 60 * 60,
    witness_vout: 1,
  })
  loading.value = false
  if (err || !result?.invoice) {
    errorMessage.value = err?.message || t('rgb11Invoice.generateFailed')
    return
  }
  invoice.value = result.invoice
  requestId.value = result.request_id || result.requestId || ''
}

const checkProxyTransfer = async () => {
  loading.value = true
  errorMessage.value = ''
  try {
    const [err, result] = await walletManager.receiveRGB11ProxyConsignment(requestId.value)
    if (err || !result?.ack_posted) throw err || new Error(t('rgb11Invoice.receiveFailed'))
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

const acceptPackage = async () => {
  loading.value = true
  errorMessage.value = ''
  try {
	const input = transferPackage.value.trim()
	let parsed: any
	try {
	  parsed = JSON.parse(input)
	} catch {
	  parsed = { transport_mode: 'out-of-band', consignment: input }
	}
	if (parsed?.transport_mode !== 'out-of-band' || !parsed?.consignment) {
	  throw new Error(t('rgb11Transfer.invalidPackage'))
	}
	const [err, result] = await walletManager.acceptRGB11Consignment(requestId.value, parsed.consignment)
	if (err || !result) throw err || new Error(t('rgb11Transfer.acceptFailed'))
    await refreshRGB11State()
	toast({ title: t('rgb11Transfer.acceptedOutOfBand'), variant: 'success', duration: 2500 })
  } catch (error: any) {
    errorMessage.value = error?.message || t('rgb11Transfer.acceptFailed')
  } finally {
    loading.value = false
  }
}

watch(isOpen, (open) => {
  if (open) {
    void loadProxyEndpoint()
  } else {
    amount.value = ''
    receiveMode.value = 'witness'
    transportMode.value = 'out-of-band'
    invoice.value = ''
    requestId.value = ''
    transferPackage.value = ''
    errorMessage.value = ''
    loading.value = false
  }
})
</script>
