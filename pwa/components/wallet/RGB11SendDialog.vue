<template>
  <Dialog :open="isOpen" @update:open="handleOpenChange">
    <DialogContent class="max-h-[calc(100dvh_-_2rem)] w-[calc(100%_-_2rem)] grid-cols-[minmax(0,1fr)] gap-4 overflow-y-auto rounded-xl bg-zinc-950"
      @escape-key-down="preventDismiss" @interact-outside="preventDismiss">
      <DialogHeader class="mb-0 pr-8 text-left">
        <DialogTitle>{{ t(inflation ? 'rgb11Transfer.inflate' : 'rgb11Transfer.sendTitle') }}</DialogTitle>
        <DialogDescription v-if="!transfer && asset?.label" class="break-words">{{ asset.label }}</DialogDescription>
      </DialogHeader>
      <div class="min-w-0 space-y-4">
        <template v-if="!transfer">
          <select v-if="!inflation" v-model="mode" :disabled="loading"
            :aria-label="t('rgb11Transfer.destinationType')"
            class="h-10 w-full rounded-md border border-zinc-700 bg-zinc-900 px-3 text-sm">
            <option value="address">{{ t('rgb11Transfer.addressMode') }}</option>
            <option value="invoice">{{ t('rgb11Transfer.invoiceMode') }}</option>
          </select>
          <div class="space-y-2">
            <Label :for="fieldId + '-destination'">{{ t(addressMode ? 'rgb11Transfer.receiverAddress' : 'rgb11Transfer.invoice') }}</Label>
            <Textarea :id="fieldId + '-destination'" v-model="destination" :disabled="loading" spellcheck="false"
              :rows="addressMode ? 2 : 3" class="min-h-0 bg-zinc-900 font-mono text-xs" />
            <p v-if="!addressMode" class="text-xs text-muted-foreground">{{ t('rgb11Transfer.batchHint') }}</p>
          </div>
          <div v-if="addressMode" class="space-y-2">
            <Label :for="fieldId + '-amount'">{{ t('rgb11Invoice.amount') }}</Label>
            <Input :id="fieldId + '-amount'" v-model="amount" :disabled="loading" inputmode="decimal" />
          </div>
        </template>
        <template v-else>
          <div class="flex flex-wrap items-start justify-between gap-3 rounded-lg border border-zinc-800 bg-zinc-900 p-3">
            <p role="status" class="min-w-0 flex-1 basis-48 text-sm font-medium">
              {{ uncertain ? t('rgb11Transfer.confirmUnverified') : displayStatus(transfer.status) }}
            </p>
            <p class="shrink-0 text-sm text-muted-foreground">
              {{ t('common.fee') }} <span class="ml-1 font-medium tabular-nums text-foreground">{{ transfer.fee_sats }} sats</span>
            </p>
          </div>
          <p v-for="warning in transfer.warning_codes" :key="warning" class="text-xs text-amber-400">{{ displayWarning(warning) }}</p>
          <ul class="space-y-3">
            <li v-for="(recipient, index) in transfer.recipients" :key="index"
              class="min-w-0 space-y-3 rounded-lg border border-zinc-800 p-3">
              <div class="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
                <span class="text-sm text-muted-foreground">{{ t('rgb11Transfer.recipientNumber', { number: index + 1 }) }}</span>
              </div>
              <dl class="space-y-2 text-sm">
                <div>
                  <dt class="text-muted-foreground">{{ t('tools.txConfirm.asset') }}</dt>
                  <dd class="break-all font-mono">{{ recipient.amount.asset }}</dd>
                </div>
                <div>
                  <dt class="text-muted-foreground">{{ t('rgb11Invoice.amount') }}</dt>
                  <dd class="break-all font-semibold tabular-nums">{{ recipient.amount.amount }}</dd>
                </div>
                <div>
                  <dt class="text-muted-foreground">{{ t('tools.txConfirm.to') }}</dt>
                  <dd class="break-all font-mono">{{ recipient.destination }}</dd>
                </div>
              </dl>
              <div v-if="transfer.needs_receipts" class="space-y-2">
                <Label :for="fieldId + '-receipt-' + index" class="text-xs">
                  {{ t('rgb11Transfer.recipientSummary', { number: index + 1 }) }}
                </Label>
                <Textarea :id="fieldId + '-receipt-' + index" v-model="receipts[index]" :disabled="loading"
                  spellcheck="false" rows="2" class="min-h-0 bg-zinc-900 font-mono text-xs" />
              </div>
            </li>
          </ul>
          <div v-if="transfer.can_export || files.length" class="flex flex-wrap gap-2">
            <Button v-if="transfer.can_export" variant="outline" size="sm" :disabled="loading" @click="exportTransfer">
              {{ t('rgb11Transfer.downloadConsignment') }}
            </Button>
            <Button v-for="item in files" :key="item.recipient_index + '-' + item.file.name" variant="outline" size="sm"
              :title="item.file.name" @click="saveExportFile(item.file)">
              {{ t('rgb11Transfer.recipientNumber', { number: item.recipient_index + 1 }) }} · .{{ item.file.name.split('.').at(-1) }}
            </Button>
          </div>
        </template>
        <details v-if="(!transfer && asset?.contract_id) || (transfer && (transfer.status !== 'preview' || uncertain))"
          class="min-w-0 rounded-lg border border-zinc-800 px-3 py-2 text-sm text-muted-foreground">
          <summary class="cursor-pointer font-medium">{{ t('rgb11Transfer.transferDetails') }}</summary>
          <dl class="mt-2 space-y-2 text-xs">
            <div v-if="!transfer && asset?.contract_id">
              <dt>{{ t('rgb11Transfer.contractId') }}</dt>
              <dd class="break-all font-mono">{{ asset.contract_id }}</dd>
            </div>
            <div v-if="transfer && (transfer.status !== 'preview' || uncertain)">
              <dt>{{ t('rgb11Transfer.transferId') }}</dt>
              <dd class="break-all font-mono">{{ transfer.id }}</dd>
            </div>
            <div v-if="transfer?.tx_id && transfer.status !== 'preview'">
              <dt>{{ t('rgb11Transfer.txId') }}</dt>
              <dd class="break-all font-mono">{{ transfer.tx_id }}</dd>
            </div>
          </dl>
        </details>
        <p v-if="message" role="alert" class="break-all text-sm text-red-400">{{ message }}</p>
        <Button v-if="!transfer" :disabled="loading || !destination.trim() || !account.rgb11WriteAllowed"
          class="w-full" @click="prepare">{{ t('rgb11Transfer.prepare') }}</Button>
        <div v-else-if="transfer.can_confirm || (transfer.can_cancel && !uncertain)"
          class="flex justify-end gap-2 border-t border-zinc-800 pt-3">
          <Button v-if="transfer.can_cancel && !uncertain" variant="outline" :disabled="loading" @click="cancel">{{ t('common.cancel') }}</Button>
          <Button v-if="transfer.can_confirm" :disabled="loading" class="flex-1 sm:flex-none" @click="confirm">{{ t('common.confirm') }}</Button>
        </div>
      </div>
    </DialogContent>
  </Dialog>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import walletManager from '@/utils/sat20'
import { saveExportFile } from '@/utils/accountManagement'
import type { RGBTransferView, RGBTransferFile } from '@/utils/walletViews'
import { useWalletStore } from '@/store/wallet'
import { useAccountManagementStore } from '@/store/accountManagement'
import { subscribeWalletIdentity } from '@/lib/identity-boundary'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
const props = defineProps<{asset: {key?: string; asset?: string; contract_id?: string; label?: string} | null; resumeTransferId?: string; inflation?: boolean}>()
const emit = defineEmits<{(event: 'completed'): void}>()
const isOpen = defineModel('open', {type: Boolean})
const {t, te} = useI18n()
const account = useAccountManagementStore()
const wallet = useWalletStore()
const mode = ref('address')
const addressMode = computed(() => mode.value === 'address' && !props.inflation)
const fieldId = useId()
const destination = ref('')
const amount = ref('')
const transfer = ref<RGBTransferView | null>(null)
const receipts = ref<string[]>([])
const files = ref<RGBTransferFile[]>([])
const loading = ref(false)
const preparing = ref(false)
const message = ref('')
const uncertain = ref(false)
const assetKey = () => props.asset?.key || props.asset?.asset || props.asset?.contract_id || ''
const handleOpenChange = (open: boolean) => {
  if (!open && preparing.value) return
  isOpen.value = open
}
const preventDismiss = (event: Event) => {
  if (preparing.value) event.preventDefault()
}
const displayStatus = (value: string) => value === 'preview' ? t('rgb11Transfer.preview') : te(`walletComponents.states_${value}`) ? t(`walletComponents.states_${value}`) : value
const displayWarning = (value: string) => value === 'rgb_address_carrier' ? t('rgb11Transfer.carrierWarning') : value
let generation = 0
const run = async (action: () => Promise<void>) => {
  if (loading.value) return
  const actionGeneration = generation
  loading.value = true; message.value = ''
  try { await action() } catch (error) {
    if (actionGeneration === generation && isOpen.value) message.value = error instanceof Error ? error.message : String(error)
  }
  finally { loading.value = false }
}
// A preview belongs only to this open dialog. Once confirmation starts, the SDK
// owns the transfer lifecycle; closing must never cancel a possibly saved send.
const confirming = new Set<string>()
const discardPreview = (value: RGBTransferView | null) => {
  if (value?.status === 'preview' && !confirming.has(value.id)) {
    void walletManager.cancelRGB11Transfer(value.id).catch(() => {})
  }
}
const prepare = () => run(async () => {
  const preparedGeneration = generation
  preparing.value = true
  try {
    const result = props.inflation
      ? await walletManager.prepareRGB11Inflation(destination.value.trim(), Number(wallet.btcFeeRate || 1))
      : await walletManager.prepareRGB11Transfer({fee_rate: Number(wallet.btcFeeRate || 1), recipients: mode.value === 'address'
        ? [{destination: destination.value.trim(), asset: props.asset?.key || props.asset?.asset || '', amount: amount.value.trim()}]
        : destination.value.split('\n').map(value => value.trim()).filter(Boolean).map(value => ({destination: value, asset: '', amount: ''}))})
    if (result[0] || !result[1]) throw result[0] || new Error(t('rgb11Transfer.prepareFailed'))
    if (preparedGeneration !== generation || !isOpen.value) {
      discardPreview(result[1])
      return
    }
    transfer.value = result[1]; receipts.value = transfer.value.recipients.map(() => '')
  } finally {
    preparing.value = false
  }
})
const confirm = () => run(async () => {
  if (!transfer.value) return
  const confirmedGeneration = generation
  const id = transfer.value.id
  const preview = transfer.value.status === 'preview'
  if (preview) confirming.add(id)
  const [error, value] = await walletManager.confirmRGB11Transfer(id, receipts.value)
  if (error || !value) {
    if (confirmedGeneration !== generation || !isOpen.value || transfer.value?.id !== id) return
    if (preview) {
      // Delivery may fail after this exact candidate was saved. Read its current
      // view; never prepare a replacement or claim an unknown result is unsaved.
      uncertain.value = true
      const [readError, snapshot] = await walletManager.getRGB11View()
      if (confirmedGeneration !== generation || !isOpen.value || transfer.value?.id !== id) return
      if (!readError && snapshot) {
        const saved = snapshot.transfers.find(item => item.id === id)
        uncertain.value = false
        if (saved) { transfer.value = saved; emit('completed') }
        else confirming.delete(id)
      }
    }
    throw error || new Error(t('rgb11Transfer.broadcastFailed'))
  }
  confirming.delete(id)
  if (confirmedGeneration === generation && isOpen.value) {
    transfer.value = value; uncertain.value = false
  }
  emit('completed')
})
const cancel = () => run(async () => {
  if (!transfer.value) return
  const preview = transfer.value.status === 'preview'
  if (!preview && !window.confirm(t('rgb11Transfer.taskCancelConfirm'))) return
  const [error, value] = await walletManager.cancelRGB11Transfer(transfer.value.id)
  if (error || !value) throw error || new Error(t('rgb11Transfer.taskCancelFailed'))
  uncertain.value = false
  if (preview) {
    transfer.value = null; receipts.value = []; files.value = []
  } else {
    transfer.value = value; emit('completed')
  }
})
const exportTransfer = () => run(async () => {
  if (!transfer.value) return
  const [error, value] = await walletManager.exportRGB11Transfer(transfer.value.id)
  if (error) throw error
  files.value = value || []
})
const unsubscribe = subscribeWalletIdentity(identity => {
  if (identity.phase === 'QUIESCING') isOpen.value = false
})
onBeforeUnmount(() => {
  unsubscribe()
  generation++
  discardPreview(transfer.value)
})
watch([isOpen, assetKey], ([open]) => {
  generation++
  if (!open) {
    discardPreview(transfer.value)
    if (transfer.value?.status === 'preview') transfer.value = null
    return
  }
  discardPreview(transfer.value)
  message.value = ''; files.value = []; receipts.value = []; transfer.value = null; uncertain.value = false
  destination.value = ''; amount.value = ''; mode.value = props.inflation ? 'invoice' : 'address'
  if (props.resumeTransferId) void run(async () => {
    const [error, value] = await walletManager.getRGB11View()
    if (error) throw error
    transfer.value = value?.transfers.find(item => item.id === props.resumeTransferId) || null
    if (!transfer.value) throw new Error(t('rgb11Transfer.taskResumeFailed'))
    receipts.value = transfer.value.recipients.map(() => '')
  })
}, {immediate: true, flush: 'sync'})
</script>
