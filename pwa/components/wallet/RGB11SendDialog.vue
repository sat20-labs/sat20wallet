<template>
  <Dialog :open="isOpen" @update:open="isOpen = $event">
    <DialogContent class="max-h-[90vh] w-[380px] overflow-y-auto rounded-lg bg-black">
      <DialogHeader>
        <DialogTitle>{{ $t('rgb11Transfer.sendTitle') }}</DialogTitle>
        <DialogDescription class="break-all font-mono text-xs">
          {{ assetContractID }}
        </DialogDescription>
      </DialogHeader>

      <div class="space-y-4">
        <div class="grid grid-cols-2 gap-2 rounded-lg bg-zinc-900 p-1">
          <button
            type="button"
            class="rounded-md px-3 py-2 text-xs"
            :class="transferMode === 'address' ? 'bg-zinc-700 text-white' : 'text-zinc-400'"
            @click="transferMode = 'address'"
          >
            {{ $t('rgb11Transfer.addressMode') }}
          </button>
          <button
            type="button"
            class="rounded-md px-3 py-2 text-xs"
            :class="transferMode === 'invoice' ? 'bg-zinc-700 text-white' : 'text-zinc-400'"
            @click="transferMode = 'invoice'"
          >
            {{ $t('rgb11Transfer.invoiceMode') }}
          </button>
        </div>

        <template v-if="transferMode === 'address'">
          <div class="rounded-md border border-amber-800 bg-amber-950/40 p-3 text-xs text-amber-300">
            {{ carrierWarning || $t('rgb11Transfer.carrierWarning') }}
          </div>

          <div class="space-y-2">
            <Label>{{ $t('rgb11Transfer.receiverAddress') }}</Label>
            <input
              v-model.trim="receiverAddress"
              type="text"
              spellcheck="false"
              autocomplete="off"
              class="h-10 w-full rounded-md border border-zinc-700 bg-zinc-900 px-3 font-mono text-xs outline-none focus:border-zinc-500"
              :placeholder="$t('rgb11Transfer.receiverAddressPlaceholder')"
            />
          </div>

          <div class="space-y-2">
            <Label>{{ $t('rgb11Transfer.amountRaw') }}</Label>
            <input
              v-model.trim="amountRaw"
              type="text"
              inputmode="numeric"
              autocomplete="off"
              class="h-10 w-full rounded-md border border-zinc-700 bg-zinc-900 px-3 font-mono text-xs outline-none focus:border-zinc-500"
              placeholder="1"
            />
            <p class="text-xs text-zinc-500">{{ $t('rgb11Transfer.addressModeHint') }}</p>
          </div>

          <Button
            class="w-full"
            :disabled="loading || !receiverAddress || !validRawAmount"
            @click="sendByAddress"
          >
            {{ loading ? $t('rgb11Transfer.broadcasting') : $t('rgb11Transfer.sendByAddress') }}
          </Button>

          <p v-if="temporaryDelivery" class="text-xs text-amber-500">
            {{ $t('rgb11Transfer.temporaryTtlWarning') }}
          </p>
        </template>

        <template v-else>
          <div class="space-y-2">
            <Label>{{ $t('rgb11Transfer.invoice') }}</Label>
            <Textarea v-model="invoice" spellcheck="false" class="min-h-28 bg-zinc-900 font-mono text-xs" />
            <p class="text-xs text-zinc-500">{{ $t('rgb11Transfer.batchHint') }}</p>
          </div>

          <Button class="w-full" :disabled="loading || !invoice.trim() || !!transferId" @click="prepareTraditional">
            {{ loading ? $t('rgb11Transfer.preparing') : $t('rgb11Transfer.prepare') }}
          </Button>

          <div v-if="transferPackage" class="space-y-2">
            <Label>{{ $t('rgb11Transfer.package') }}</Label>
            <Textarea
              :model-value="transferPackage"
              readonly
              spellcheck="false"
              class="min-h-36 bg-zinc-900 font-mono text-xs"
            />
            <Button variant="outline" class="w-full" @click="copyText(transferPackage)">
              <Icon icon="lucide:copy" class="mr-2 h-4 w-4" />
              {{ $t('rgb11Transfer.copyPackage') }}
            </Button>
            <p class="text-xs text-amber-500">{{ $t('rgb11Transfer.ackGate') }}</p>
          </div>

          <div v-if="transferId" class="space-y-2">
            <Label>{{ $t('rgb11Transfer.ack') }}</Label>
            <p v-if="outOfBand" class="text-xs text-amber-500">{{ $t('rgb11Transfer.outOfBandAck') }}</p>
            <Button v-if="!outOfBand" variant="outline" class="w-full" :disabled="loading" @click="fetchAck">
              {{ $t('rgb11Transfer.fetchAck') }}
            </Button>
            <Textarea v-if="!outOfBand" v-model="ack" spellcheck="false" class="min-h-28 bg-zinc-900 font-mono text-xs" />
            <Button class="w-full" :disabled="loading || (!outOfBand && !ack.trim())" @click="broadcastTraditional">
              {{ loading ? $t('rgb11Transfer.broadcasting') : $t('rgb11Transfer.broadcast') }}
            </Button>
          </div>
        </template>

        <p v-if="message" class="break-all text-xs" :class="success ? 'text-emerald-400' : 'text-red-400'">
          {{ message }}
        </p>
      </div>
    </DialogContent>
  </Dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Icon } from '@iconify/vue'
import { useClipboard } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import { storeToRefs } from 'pinia'
import walletManager from '@/utils/sat20'
import rgb11Address from '@/utils/rgb11Address'
import { useWalletStore } from '@/store'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'

type RGB11Asset = {
  key?: string
  protocol?: string
  type?: string
  ticker?: string
  contract_id?: string
}

const props = defineProps<{ asset: RGB11Asset | null }>()
const emit = defineEmits<{ (e: 'completed'): void }>()
const isOpen = defineModel('open', { type: Boolean })
const transferMode = ref<'address' | 'invoice'>('address')
const receiverAddress = ref('')
const amountRaw = ref('')
const temporaryDelivery = ref(false)
const carrierWarning = ref('')
const invoice = ref('')
const ack = ref('')
const transferId = ref('')
const relayRecord = ref('')
const transferPackage = ref('')
const loading = ref(false)
const message = ref('')
const success = ref(false)
const pendingPrepared = ref<any>(null)
const batchItems = ref<Array<{ transferId: string; relayRecord: string; transportMode: string }>>([])
const outOfBand = ref(false)
const { copy } = useClipboard()
const { t } = useI18n()
const walletStore = useWalletStore()
const { btcFeeRate } = storeToRefs(walletStore)

const assetContractID = computed(() => {
  const value = props.asset?.contract_id || props.asset?.ticker || ''
  return value.startsWith('rgb:') ? value : `rgb:${value}`
})

const assetName = computed(() => {
  if (props.asset?.key?.startsWith('rgb11:')) return props.asset.key
  return `${props.asset?.protocol || 'rgb11'}:${props.asset?.type || 'f'}:${props.asset?.ticker || ''}`
})

const validRawAmount = computed(() => /^[1-9]\d*$/.test(amountRaw.value))

const loadCarrierWarning = async () => {
  const [, result] = await rgb11Address.carrierWarning()
  carrierWarning.value = result?.warning || ''
}

const sendByAddress = async () => {
  loading.value = true
  message.value = ''
  success.value = false
  temporaryDelivery.value = false
  try {
    const [prepareErr, prepareResult] = await rgb11Address.prepareTransfer({
      receiver_address: receiverAddress.value,
      asset_name: assetName.value,
      amount_raw: amountRaw.value,
      fee_rate: Number(btcFeeRate.value || 1),
      min_confirmations: 1,
    })
    if (prepareErr || !prepareResult?.transfer) {
      const reason = prepareErr?.message || t('rgb11Transfer.prepareFailed')
      if (/traditional RGB invoice|no RGB11 DKVS address capability/i.test(reason)) {
        transferMode.value = 'invoice'
        throw new Error(t('rgb11Transfer.addressUnavailable'))
      }
      throw prepareErr || new Error(reason)
    }
    const prepared = JSON.parse(prepareResult.transfer)
    const id = prepared?.state?.transfer_id
    if (!id) throw new Error(t('rgb11Transfer.prepareFailed'))

    const [sendErr, sendResult] = await rgb11Address.deliverAndBroadcast({
      transfer_id: id,
    })
    if (sendErr || !sendResult?.txid) throw sendErr || new Error(t('rgb11Transfer.broadcastFailed'))
    temporaryDelivery.value = !!sendResult.temporary
    success.value = true
    message.value = t('rgb11Transfer.addressBroadcasted', { txid: sendResult.txid })
    await walletManager.refreshRGB11State()
    emit('completed')
  } catch (error: any) {
    message.value = error?.message || t('rgb11Transfer.broadcastFailed')
  } finally {
    loading.value = false
  }
}

const prepareTraditional = async () => {
  loading.value = true
  message.value = ''
  success.value = false
  if (!pendingPrepared.value) {
    const invoices = invoice.value.split(/\r?\n/).map((value) => value.trim()).filter(Boolean)
    const [err, result] = await walletManager.prepareRGB11Transfer({
      ...(invoices.length === 1 ? { invoice: invoices[0] } : { invoices }),
      fee_rate: Number(btcFeeRate.value || 1),
      min_confirmations: 1,
    })
    if (err || !result?.transfer) {
      loading.value = false
      message.value = err?.message || t('rgb11Transfer.prepareFailed')
      return
    }
    pendingPrepared.value = JSON.parse(result.transfer)
  }
  try {
    const prepared = pendingPrepared.value
    const states = Array.isArray(prepared?.states) && prepared.states.length ? prepared.states : [prepared?.state]
    if (!states.length || states.some((state: any) => !state?.transfer_id)) throw new Error(t('rgb11Transfer.prepareFailed'))
    const items: Array<{ transferId: string; relayRecord: string; transportMode: string }> = []
    const packages = []
    for (const state of states) {
      const transportMode = state.transport_mode || 'sat20-dkvs'
      if (transportMode === 'out-of-band') {
        items.push({ transferId: state.transfer_id, relayRecord: '', transportMode })
        packages.push({
          version: 1,
          transport_mode: transportMode,
          transfer_id: state.transfer_id,
          recipient_id: state.recipient_id,
          consignment: prepared.recipient_consignment,
        })
      } else {
        const [recordErr, recordResult] = await walletManager.publishRGB11RelayRecord(state.transfer_id)
        if (recordErr || !recordResult?.record) throw recordErr || new Error(t('rgb11Transfer.prepareFailed'))
        items.push({ transferId: state.transfer_id, relayRecord: recordResult.record, transportMode })
        packages.push({
          version: 1,
          transport_mode: transportMode,
          transfer_id: state.transfer_id,
          relay_record: JSON.parse(recordResult.record),
          consignment: prepared.recipient_consignment,
        })
      }
    }
    batchItems.value = items
    outOfBand.value = items.every((item) => item.transportMode === 'out-of-band')
    transferId.value = items[0].transferId
    relayRecord.value = items[0].relayRecord
    transferPackage.value = JSON.stringify(packages.length === 1 ? packages[0] : packages)
    success.value = true
    message.value = t(outOfBand.value ? 'rgb11Transfer.preparedOutOfBand' : 'rgb11Transfer.prepared')
    pendingPrepared.value = null
  } catch (error: any) {
    message.value = error?.message || t('rgb11Transfer.prepareFailed')
  } finally {
    loading.value = false
  }
}

const fetchAck = async () => {
  loading.value = true
  message.value = ''
  success.value = false
  try {
    const decisions = await Promise.all(batchItems.value.map(async (item) => {
      const [err, result] = await walletManager.fetchRGB11AckRecord(item.transferId)
      return { item, err, result }
    }))
    const fetched = []
    for (const decision of decisions) {
      if (decision.err || !decision.result?.ack) continue
      const ackRecord = JSON.parse(decision.result.ack)
      if (ackRecord.accepted === false) {
        const [cancelErr] = await walletManager.cancelRGB11BatchByNack(
          decision.item.transferId, decision.item.relayRecord, JSON.stringify(ackRecord),
        )
        if (cancelErr) throw cancelErr
        ack.value = JSON.stringify({ ack: ackRecord })
        message.value = t('rgb11Transfer.senderRejected', { reason: ackRecord.reason_code || 'rejected' })
        await walletManager.refreshRGB11State()
        emit('completed')
        return
      }
      fetched.push({ transfer_id: decision.item.transferId, ack: ackRecord })
    }
    const unavailable = decisions.find((decision) => decision.err || !decision.result?.ack)
    if (unavailable) throw unavailable.err || new Error(t('rgb11Transfer.fetchAckFailed'))
    ack.value = JSON.stringify(fetched.length === 1 ? { ack: fetched[0].ack } : { acks: fetched })
    success.value = true
    message.value = t('rgb11Transfer.ackFetched')
  } catch (error: any) {
    message.value = error?.message || t('rgb11Transfer.fetchAckFailed')
  } finally {
    loading.value = false
  }
}

const broadcastTraditional = async () => {
  loading.value = true
  message.value = ''
  success.value = false
  try {
    const parsed = outOfBand.value ? null : JSON.parse(ack.value.trim())
    let err: Error | undefined
    let result: { txid: string } | undefined
    if (outOfBand.value) {
      ;[err, result] = await walletManager.broadcastRGB11OutOfBand(
        batchItems.value.map((item) => item.transferId),
      )
    } else if (batchItems.value.length === 1) {
      const ackRecord = parsed?.ack || parsed
      ;[err, result] = await walletManager.broadcastRGB11Transfer(
        transferId.value,
        relayRecord.value,
        JSON.stringify(ackRecord),
      )
    } else {
      const supplied = Array.isArray(parsed?.acks) ? parsed.acks : (Array.isArray(parsed) ? parsed : [])
      const byTransfer = new Map(supplied.map((item: any) => [item?.transfer_id, item?.ack || item]))
      const ackRecords = batchItems.value.map((item) => byTransfer.get(item.transferId))
      if (ackRecords.some((item) => !item)) throw new Error(t('rgb11Transfer.fetchAckFailed'))
      ;[err, result] = await walletManager.broadcastRGB11Batch({
        transfer_ids: batchItems.value.map((item) => item.transferId),
        relay_records: batchItems.value.map((item) => JSON.parse(item.relayRecord)),
        acks: ackRecords,
      })
    }
    if (err || !result?.txid) throw err || new Error(t('rgb11Transfer.broadcastFailed'))
    success.value = true
    message.value = t('rgb11Transfer.broadcasted', { txid: result.txid })
    await walletManager.refreshRGB11State()
    emit('completed')
  } catch (error: any) {
    message.value = error?.message || t('rgb11Transfer.broadcastFailed')
  } finally {
    loading.value = false
  }
}

const copyText = async (value: string) => {
  await copy(value)
  message.value = t('rgb11Transfer.copied')
  success.value = true
}

watch(isOpen, (open) => {
  if (open) {
    void loadCarrierWarning()
    return
  }
  transferMode.value = 'address'
  receiverAddress.value = ''
  amountRaw.value = ''
  temporaryDelivery.value = false
  carrierWarning.value = ''
  invoice.value = ''
  ack.value = ''
  transferId.value = ''
  relayRecord.value = ''
  transferPackage.value = ''
  loading.value = false
  message.value = ''
  success.value = false
  pendingPrepared.value = null
  batchItems.value = []
  outOfBand.value = false
})
</script>
