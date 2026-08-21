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
            <Label>{{ $t('rgb11Transfer.amountRaw') }}</Label>
            <input
              v-model.trim="amountRaw"
              type="text"
              inputmode="numeric"
              autocomplete="off"
              class="h-10 w-full rounded-md border border-zinc-700 bg-zinc-900 px-3 font-mono text-xs outline-none focus:border-zinc-500"
              placeholder="1"
            />
          </div>

          <div class="space-y-2">
            <Label>{{ $t('rgb11Transfer.invoice') }}</Label>
            <Textarea v-model="invoice" spellcheck="false" class="min-h-28 bg-zinc-900 font-mono text-xs" />
            <p class="text-xs text-zinc-500">{{ $t('rgb11Transfer.batchHint') }}</p>
          </div>

          <Button
            class="w-full"
            :disabled="loading || !invoice.trim() || !traditionalAmountValid || !!transferId"
            @click="prepareTraditional"
          >
            {{ loading ? $t('rgb11Transfer.preparing') : $t('rgb11Transfer.prepare') }}
          </Button>

          <div v-if="outOfBand && standardConsignmentBase64" class="space-y-2">
            <Label>{{ $t('rgb11Transfer.standardConsignment') }}</Label>
            <Button variant="outline" class="w-full" @click="downloadStandardConsignment">
              {{ $t('rgb11Transfer.downloadConsignment') }}
            </Button>
          </div>

          <div v-if="transferId" class="space-y-2">
            <Label>{{ $t('rgb11Transfer.ack') }}</Label>
            <p v-if="outOfBand" class="text-xs text-amber-500">{{ $t('rgb11Transfer.outOfBandAck') }}</p>
            <p v-if="proxyTransport && proxyBroadcasted" class="text-xs text-zinc-500">
              {{ $t('rgb11Transfer.proxyAckHelp') }}
            </p>
            <Button v-if="proxyTransport && !proxyBroadcasted" variant="outline" class="w-full"
              :disabled="loading" @click="deliverProxyTransfer()">
              {{ $t('rgb11Transfer.retryProxyDelivery') }}
            </Button>
            <Button v-if="proxyTransport && proxyBroadcasted" variant="outline" class="w-full"
              :disabled="loading" @click="checkProxyAck">
              {{ $t('rgb11Transfer.checkProxyAck') }}
            </Button>
            <Button v-if="outOfBand" class="w-full" :disabled="loading" @click="broadcastTraditional">
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
import { useI18n } from 'vue-i18n'
import { storeToRefs } from 'pinia'
import walletManager from '@/utils/sat20'
import rgb11Address from '@/utils/rgb11Address'
import { beginNamedPwaOperation, finishPwaOperation, type PwaOperationContext } from '@/utils/pwaOperationLog'
import { updateOperationLog } from '@/utils/operationLog'
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
const transferId = ref('')
const standardConsignmentBase64 = ref('')
const loading = ref(false)
const message = ref('')
const success = ref(false)
const pendingPrepared = ref<any>(null)
const batchItems = ref<Array<{ transferId: string; transportMode: string }>>([])
const outOfBand = ref(false)
const proxyTransport = ref(false)
const proxyBroadcasted = ref(false)
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
const invoiceCount = computed(() => invoice.value.split(/\r?\n/).map((value) => value.trim()).filter(Boolean).length)
const traditionalAmountValid = computed(() => invoiceCount.value > 1 || validRawAmount.value)

const loadCarrierWarning = async () => {
  const [, result] = await rgb11Address.carrierWarning()
  carrierWarning.value = result?.warning || ''
}

const sendByAddress = async () => {
  loading.value = true
  message.value = ''
  success.value = false
  temporaryDelivery.value = false
  const operation = await beginNamedPwaOperation({
    category: 'rgb11',
    action: 'rgb11_send_address',
    title: 'Send RGB asset',
    summary: 'Sending an RGB11 asset to a wallet address',
    parameters: {
      contract_id: assetContractID.value,
      asset: assetName.value,
      amount_raw: amountRaw.value,
      destination: receiverAddress.value,
      fee_rate: String(Number(btcFeeRate.value || 1)),
    },
    successMessage: 'RGB address transfer broadcast',
  })
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
      if (/traditional RGB invoice|no RGB11 .*address capability/i.test(reason)) {
        transferMode.value = 'invoice'
        throw new Error(t('rgb11Transfer.addressUnavailable'))
      }
      throw prepareErr || new Error(reason)
    }
    const prepared = JSON.parse(prepareResult.transfer)
    const id = prepared?.state?.transfer_id
    if (!id) throw new Error(t('rgb11Transfer.prepareFailed'))
    if (operation) {
      await updateOperationLog(operation.id, {
        status: 'running',
        message: 'RGB transfer prepared',
        details: { transfer_id: id },
      })
    }

    const [sendErr, sendResult] = await rgb11Address.deliverAndBroadcast({
      transfer_id: id,
    })
    if (sendErr || !sendResult?.txid) throw sendErr || new Error(t('rgb11Transfer.broadcastFailed'))
    temporaryDelivery.value = !!sendResult.temporary
    await completeBroadcast(sendResult.txid, 'rgb11Transfer.addressBroadcasted', operation, id)
  } catch (error: any) {
    const sendError = error instanceof Error ? error : new Error(error?.message || t('rgb11Transfer.broadcastFailed'))
    await finishPwaOperation(operation, sendError)
    message.value = sendError.message
  } finally {
    loading.value = false
  }
}

const prepareTraditional = async () => {
  loading.value = true
  message.value = ''
  success.value = false
  const operation = await beginNamedPwaOperation({
    category: 'rgb11',
    action: 'rgb11_prepare_send',
    title: 'Prepare RGB transfer',
    summary: 'Preparing an RGB11 transfer from the send dialog',
    parameters: {
      contract_id: assetContractID.value,
      amount_raw: amountRaw.value,
      invoice_count: String(invoiceCount.value),
      fee_rate: String(Number(btcFeeRate.value || 1)),
    },
    successMessage: 'RGB transfer prepared',
  })
  if (!pendingPrepared.value) {
    const invoices = invoice.value.split(/\r?\n/).map((value) => value.trim()).filter(Boolean)
    const [err, result] = await walletManager.prepareRGB11Transfer({
      ...(invoices.length === 1 ? { invoice: invoices[0] } : { invoices }),
      ...(invoices.length === 1 ? {
        contract_id: assetContractID.value,
        amount_raw: amountRaw.value,
      } : {}),
      fee_rate: Number(btcFeeRate.value || 1),
      min_confirmations: 1,
    })
    if (err || !result?.transfer) {
      loading.value = false
      const prepareError = err || new Error(t('rgb11Transfer.prepareFailed'))
      await finishPwaOperation(operation, prepareError)
      message.value = prepareError.message
      return
    }
    pendingPrepared.value = JSON.parse(result.transfer)
  }
  try {
    const prepared = pendingPrepared.value
    const states = Array.isArray(prepared?.states) && prepared.states.length ? prepared.states : [prepared?.state]
    if (!states.length || states.some((state: any) => !state?.transfer_id)) throw new Error(t('rgb11Transfer.prepareFailed'))
    const items: Array<{ transferId: string; transportMode: string }> = []
    for (const state of states) {
      const transportMode = state.transport_mode || ''
      if (transportMode === 'rgb-json-rpc') {
        items.push({ transferId: state.transfer_id, transportMode })
      } else if (transportMode === 'out-of-band') {
        items.push({ transferId: state.transfer_id, transportMode })
      } else {
        throw new Error(`unsupported RGB11 invoice transport: ${transportMode || 'missing'}`)
      }
    }
    batchItems.value = items
    outOfBand.value = items.every((item) => item.transportMode === 'out-of-band')
    proxyTransport.value = items.every((item) => item.transportMode === 'rgb-json-rpc')
    transferId.value = items[0].transferId
    if (operation) {
      await updateOperationLog(operation.id, {
        status: 'running',
        message: 'RGB transfer prepared',
        details: {
          transfer_id: transferId.value,
          transport_mode: items[0].transportMode,
          transfer_count: String(items.length),
        },
      })
    }
    standardConsignmentBase64.value = outOfBand.value
      ? (prepared.recipient_consignment_base64 || '')
      : ''
    if (outOfBand.value && !standardConsignmentBase64.value) {
      throw new Error(t('rgb11Transfer.prepareFailed'))
    }
    if (proxyTransport.value) {
      await deliverProxyTransfer(operation)
      return
    }
    await finishPwaOperation(operation, null, { transfer_id: transferId.value })
    success.value = true
    message.value = t(
      proxyTransport.value
        ? 'rgb11Transfer.preparedProxy'
        : (outOfBand.value ? 'rgb11Transfer.preparedOutOfBand' : 'rgb11Transfer.prepared'),
    )
    pendingPrepared.value = null
  } catch (error: any) {
    const prepareError = error instanceof Error ? error : new Error(error?.message || t('rgb11Transfer.prepareFailed'))
    await finishPwaOperation(operation, prepareError)
    message.value = prepareError.message
  } finally {
    loading.value = false
  }
}

const deliverProxyTransfer = async (parentOperation: PwaOperationContext | null = null) => {
  loading.value = true
  message.value = ''
  success.value = false
  const operation = parentOperation || await beginNamedPwaOperation({
    category: 'rgb11',
    action: 'rgb11_retry_proxy_delivery',
    title: 'Retry RGB proxy delivery',
    summary: 'Retrying delivery and broadcast for an RGB11 proxy transfer',
    parameters: {
      transfer_id: transferId.value,
      transfer_count: String(batchItems.value.length),
    },
    successMessage: 'RGB proxy transfer delivered and broadcast',
  })
  try {
    const [proxyErr, proxyResult] = await walletManager.deliverAndBroadcastRGB11ProxyTransfer(
      batchItems.value.map((item) => item.transferId),
    )
    if (proxyErr || !proxyResult?.txid) throw proxyErr || new Error(t('rgb11Transfer.broadcastFailed'))
    pendingPrepared.value = null
    proxyBroadcasted.value = true
    if (operation) {
      await updateOperationLog(operation.id, {
        status: 'running',
        message: 'RGB proxy consignment delivered; transaction broadcast',
        txid: proxyResult.txid,
        details: { txid: proxyResult.txid },
      })
    }
    await completeBroadcast(proxyResult.txid, 'rgb11Transfer.broadcasted', operation, transferId.value)
  } catch (error: any) {
    const deliveryError = error instanceof Error ? error : new Error(error?.message || t('rgb11Transfer.broadcastFailed'))
    await finishPwaOperation(operation, deliveryError)
    message.value = deliveryError.message
  } finally {
    loading.value = false
  }
}

const checkProxyAck = async () => {
  loading.value = true
  message.value = ''
  success.value = false
  try {
    const decisions = await Promise.all(batchItems.value.map(async (item) => {
      const [err, result] = await walletManager.fetchRGB11ProxyAck(item.transferId)
      return { err, result }
    }))
    const failed = decisions.find((decision) => decision.err)
    if (failed?.err) throw failed.err
    if (decisions.some((decision) => !decision.result?.available)) {
      throw new Error(t('rgb11Transfer.proxyAckPending'))
    }
    if (decisions.some((decision) => !decision.result?.accepted)) {
      message.value = t('rgb11Transfer.proxyRejected')
      await refreshRGB11State()
      emit('completed')
      return
    }
    success.value = true
    message.value = t('rgb11Transfer.proxyAccepted')
    await refreshRGB11State()
    emit('completed')
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
  const operation = await beginNamedPwaOperation({
    category: 'rgb11',
    action: 'rgb11_broadcast_out_of_band',
    title: 'Broadcast RGB transfer',
    summary: 'Broadcasting a prepared out-of-band RGB11 transfer',
    parameters: {
      transfer_id: transferId.value,
      transfer_count: String(batchItems.value.length),
    },
    successMessage: 'RGB transfer transaction broadcast',
  })
  try {
    if (!outOfBand.value) throw new Error('prepared transfer is not out-of-band')
    const [err, result] = await walletManager.broadcastRGB11OutOfBand(
      batchItems.value.map((item) => item.transferId),
    )
    if (err || !result?.txid) throw err || new Error(t('rgb11Transfer.broadcastFailed'))
    await completeBroadcast(result.txid, 'rgb11Transfer.broadcasted', operation, transferId.value)
  } catch (error: any) {
    const broadcastError = error instanceof Error ? error : new Error(error?.message || t('rgb11Transfer.broadcastFailed'))
    await finishPwaOperation(operation, broadcastError)
    message.value = broadcastError.message
  } finally {
    loading.value = false
  }
}

const completeBroadcast = async (
  txid: string,
  messageKey = 'rgb11Transfer.broadcasted',
  operation: PwaOperationContext | null = null,
  completedTransferId = '',
) => {
  success.value = true
  message.value = t(messageKey, { txid })
  await finishPwaOperation(operation, null, {
    txid,
    transfer_id: completedTransferId || transferId.value,
  })
  const [refreshErr] = await walletManager.refreshRGB11State()
  if (refreshErr) {
    message.value = t('rgb11Transfer.taskBroadcastedRefreshFailed', {
      txid,
      error: refreshErr.message,
    })
  }
  emit('completed')
}

const refreshRGB11State = async () => {
  const [err] = await walletManager.refreshRGB11State()
  if (err) throw err
}

const downloadStandardConsignment = () => {
  if (!standardConsignmentBase64.value) return
  const bytes = Uint8Array.from(atob(standardConsignmentBase64.value), (value) => value.charCodeAt(0))
  const url = URL.createObjectURL(new Blob([bytes], { type: 'application/octet-stream' }))
  const link = document.createElement('a')
  link.href = url
  link.download = `rgb11-transfer-${transferId.value.slice(0, 24) || 'consignment'}.rgbc`
  link.click()
  URL.revokeObjectURL(url)
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
  transferId.value = ''
  standardConsignmentBase64.value = ''
  loading.value = false
  message.value = ''
  success.value = false
  pendingPrepared.value = null
  batchItems.value = []
  outOfBand.value = false
  proxyTransport.value = false
  proxyBroadcasted.value = false
})
</script>
