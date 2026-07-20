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

        <div v-if="requestId" class="space-y-2 border-t border-zinc-800 pt-3">
          <Label>{{ $t('rgb11Transfer.package') }}</Label>
          <Textarea v-model="transferPackage" spellcheck="false"
            class="min-h-32 bg-zinc-900 font-mono text-xs" />
          <Button variant="outline" class="w-full" :disabled="loading || !transferPackage.trim()" @click="acceptPackage">
            {{ loading ? $t('rgb11Transfer.accepting') : $t('rgb11Transfer.accept') }}
          </Button>
          <Button variant="outline" class="w-full border-red-900 text-red-400 hover:bg-red-950"
            :disabled="loading || !transferPackage.trim()" @click="rejectPackage">
            {{ loading ? $t('rgb11Transfer.rejecting') : $t('rgb11Transfer.reject') }}
          </Button>
        </div>

        <div v-if="ack" class="space-y-2">
          <Label>{{ $t('rgb11Transfer.ack') }}</Label>
          <Textarea :model-value="ack" readonly spellcheck="false"
            class="min-h-28 bg-zinc-900 font-mono text-xs" />
          <Button variant="outline" class="w-full" @click="copyAck">
            <Icon icon="lucide:copy" class="mr-2 h-4 w-4" />
            {{ $t('rgb11Transfer.copyAck') }}
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
const invoice = ref('')
const requestId = ref('')
const transferPackage = ref('')
const ack = ref('')
const errorMessage = ref('')
const loading = ref(false)
const pendingAckValue = ref('')
const { t } = useI18n()
const { toast } = useToast()
const { copy } = useClipboard()
const assetContractID = computed(() => {
  const value = props.asset?.contract_id || props.asset?.ticker || ''
  return value.startsWith('rgb:') ? value : `rgb:${value}`
})

const maxUint64 = '18446744073709551615'

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

  loading.value = true
  errorMessage.value = ''
  const [err, result] = await walletManager.createRGB11Invoice({
    mode: receiveMode.value,
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

const copyInvoice = async () => {
  if (!invoice.value) return
  await copy(invoice.value)
  toast({ title: t('rgb11Invoice.copied'), variant: 'success', duration: 1500 })
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
	if (parsed?.transport_mode === 'out-of-band' || !parsed?.relay_record) {
	  if (!parsed?.consignment) throw new Error(t('rgb11Transfer.invalidPackage'))
	  const [err, result] = await walletManager.acceptRGB11Consignment(requestId.value, parsed.consignment)
	  if (err || !result) throw err || new Error(t('rgb11Transfer.acceptFailed'))
	  ack.value = JSON.stringify({ accepted: true, transport_mode: 'out-of-band' })
	  await walletManager.refreshRGB11State()
	  toast({ title: t('rgb11Transfer.acceptedOutOfBand'), variant: 'success', duration: 2500 })
	  return
	}
    const relayObject = typeof parsed.relay_record === 'string'
      ? JSON.parse(parsed.relay_record)
      : parsed.relay_record
    const relayRecord = JSON.stringify(relayObject)
    if (!relayObject?.ack_record_key || !parsed.consignment) throw new Error(t('rgb11Transfer.invalidPackage'))
    if (!pendingAckValue.value) {
      const [err, result] = await walletManager.acceptRGB11RelayConsignment(
        requestId.value, relayRecord, parsed.consignment,
      )
      if (err || !result?.ack) throw err || new Error(t('rgb11Transfer.acceptFailed'))
      pendingAckValue.value = JSON.stringify({ ack: JSON.parse(result.ack) })
    }
    const ackRecord = JSON.parse(pendingAckValue.value).ack
    const [publishErr] = await walletManager.publishRGB11AckRecord(
      relayObject.ack_record_key, JSON.stringify(ackRecord),
    )
    ack.value = pendingAckValue.value
    if (publishErr) throw publishErr
    pendingAckValue.value = ''
    await walletManager.refreshRGB11State()
    if (ackRecord.accepted === false) {
      toast({ title: t('rgb11Transfer.rejectedByPolicy'), variant: 'destructive', duration: 3000 })
    } else {
      toast({ title: t('rgb11Transfer.accepted'), variant: 'success', duration: 2000 })
    }
  } catch (error: any) {
    errorMessage.value = error?.message || t('rgb11Transfer.acceptFailed')
  } finally {
    loading.value = false
  }
}

const rejectPackage = async () => {
  loading.value = true
  errorMessage.value = ''
  try {
    const parsed = JSON.parse(transferPackage.value.trim())
    if (!parsed?.relay_record || !parsed?.relay_record?.ack_record_key) {
      throw new Error(t('rgb11Transfer.rejectRelayOnly'))
    }
    const relayObject = typeof parsed.relay_record === 'string'
      ? JSON.parse(parsed.relay_record)
      : parsed.relay_record
    const relayRecord = JSON.stringify(relayObject)
	if (!pendingAckValue.value) {
	  const [err, result] = await walletManager.rejectRGB11RelayConsignment(requestId.value, relayRecord)
	  if (err || !result?.ack) throw err || new Error(t('rgb11Transfer.rejectFailed'))
	  pendingAckValue.value = JSON.stringify({ ack: JSON.parse(result.ack) })
	}
	const nack = JSON.parse(pendingAckValue.value).ack
    const [publishErr] = await walletManager.publishRGB11AckRecord(
      relayObject.ack_record_key, JSON.stringify(nack),
    )
    if (publishErr) throw publishErr
    ack.value = JSON.stringify({ ack: nack })
	pendingAckValue.value = ''
    await walletManager.refreshRGB11State()
    toast({ title: t('rgb11Transfer.rejected'), variant: 'success', duration: 2000 })
  } catch (error: any) {
    errorMessage.value = error?.message || t('rgb11Transfer.rejectFailed')
  } finally {
    loading.value = false
  }
}

const copyAck = async () => {
  if (!ack.value) return
  await copy(ack.value)
  toast({ title: t('rgb11Transfer.copied'), variant: 'success', duration: 1500 })
}

watch(isOpen, (open) => {
  if (!open) {
    amount.value = ''
    receiveMode.value = 'witness'
    invoice.value = ''
    requestId.value = ''
    transferPackage.value = ''
    ack.value = ''
    errorMessage.value = ''
    loading.value = false
    pendingAckValue.value = ''
  }
})
</script>
