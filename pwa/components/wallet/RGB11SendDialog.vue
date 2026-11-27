<template>
  <Dialog :open="isOpen" @update:open="handleOpenChange">
    <DialogContent class="max-h-[calc(100dvh_-_2rem)] w-[calc(100%_-_2rem)] max-w-md grid-cols-[minmax(0,1fr)] gap-4 overflow-y-auto rounded-xl bg-background"
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
          <RGB11InvoiceGuide v-if="!addressMode && !inflation" />
          <div class="space-y-2">
            <Label :for="fieldId + '-destination'">{{ t(addressMode ? 'rgb11Transfer.receiverAddress' : 'rgb11Transfer.invoice') }}</Label>
            <Textarea :id="fieldId + '-destination'" v-model="destination" :disabled="loading" spellcheck="false"
              :rows="addressMode ? 2 : 3" class="min-h-0 bg-zinc-900 font-mono text-xs" />
            <p class="text-xs text-muted-foreground">
              {{ t(addressMode ? 'rgb11Transfer.addressModeHint' : 'rgb11Invoice.sendInvoiceHelp') }}
            </p>
            <p v-if="!addressMode && !inflation" class="text-xs text-muted-foreground">{{ t('rgb11Transfer.batchHint') }}</p>
          </div>
          <div v-if="addressMode" class="space-y-2">
            <div class="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
              <Label :for="fieldId + '-amount'">{{ t('rgb11Invoice.amount') }}</Label>
              <p :id="fieldId + '-amount-limit'" class="min-w-0 break-words text-xs tabular-nums text-muted-foreground">
                {{ maxSendAmount === undefined ? t('rgb11Transfer.maxSendUnavailable')
                  : t('rgb11Transfer.maxSendAmount', { amount: maxSendAmount }) }}
              </p>
            </div>
            <Input :id="fieldId + '-amount'" v-model="amount" :disabled="loading" inputmode="decimal"
              :aria-describedby="fieldId + '-amount-limit'" />
          </div>
        </template>
        <template v-else>
          <p class="text-sm text-muted-foreground">{{ t('tools.txConfirm.network') }} <span class="ml-1 text-foreground">Bitcoin / {{ wallet.network }}</span></p>
          <div class="flex flex-wrap items-start justify-between gap-3 rounded-lg border border-zinc-800 bg-zinc-900 p-3">
            <p role="status" class="min-w-0 flex-1 basis-48 text-sm font-medium">
              {{ uncertain ? t('rgb11Transfer.confirmUnverified') : displayStatus(transfer.status) }}
            </p>
            <p class="shrink-0 text-sm text-muted-foreground">
              {{ t('common.fee') }} <span class="ml-1 font-medium tabular-nums text-foreground">{{ transfer.fee_sats }} sats</span>
            </p>
          </div>
          <p v-for="warning in transfer.warning_codes" :key="warning" class="text-xs text-amber-400">{{ displayWarning(warning) }}</p>
          <RGB11InvoiceGuide v-if="transfer.needs_receipts && !inflation" />
          <p v-if="transfer.needs_receipts" class="text-xs leading-relaxed text-muted-foreground">
            {{ t('rgb11Invoice.sendFilesHelp') }}
          </p>
          <p v-else-if="transfer.status === 'pending_chain'" class="text-xs leading-relaxed text-muted-foreground">
            {{ t('rgb11Invoice.waitForBitcoin') }}
          </p>
          <div v-if="transfer.can_export || files.length" class="flex flex-wrap gap-2">
            <Button v-if="transfer.can_export" variant="outline" size="sm" :disabled="loading" @click="exportTransfer">
              {{ t('rgb11Transfer.downloadConsignment') }}
            </Button>
            <Button v-for="item in files" :key="item.recipient_index + '-' + item.file.name" variant="outline" size="sm"
              :disabled="loading" @click="downloadTransferFile(item.file)">
              {{ t('rgb11Transfer.downloadRecipientFile', {
                number: item.recipient_index + 1,
                extension: item.file.name.slice(item.file.name.lastIndexOf('.')),
              }) }}
            </Button>
          </div>
          <p v-if="saveNotice" role="status" class="break-words text-xs text-emerald-400">{{ saveNotice }}</p>
          <ul class="space-y-3">
            <li v-for="(recipient, index) in transfer.recipients" :key="index"
              class="min-w-0 space-y-3 rounded-lg border border-zinc-800 p-3">
              <div class="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
                <span class="text-sm text-muted-foreground">{{ t('rgb11Transfer.recipientNumber', { number: index + 1 }) }}</span>
              </div>
              <dl class="space-y-2 text-sm">
                <div>
                  <dt class="text-muted-foreground">{{ t('tools.txConfirm.to') }}</dt>
                  <dd class="break-all font-mono">{{ recipient.destination }}</dd>
                </div>
                <div class="grid grid-cols-2 gap-3">
                  <div>
                    <dt class="text-muted-foreground">{{ t('rgb11Invoice.amount') }}</dt>
                    <dd class="break-all font-semibold tabular-nums">{{ recipient.amount.amount }}</dd>
                  </div>
                  <div>
                    <dt class="text-muted-foreground">{{ t('tools.txConfirm.asset') }}</dt>
                    <dd class="break-all font-mono">{{ assetApprovalLabel(recipient.amount.asset, rgb11.state.assets) }}</dd>
                  </div>
                </div>
              </dl>
              <div v-if="transfer.needs_receipts" class="space-y-2">
                <Label :for="fieldId + '-receipt-' + index" class="text-xs">
                  {{ t('rgb11Transfer.recipientSummary', { number: index + 1 }) }}
                </Label>
                <Textarea :id="fieldId + '-receipt-' + index" v-model="receipts[index]" :disabled="loading"
                  :placeholder="t('rgb11Transfer.pasteRecipientSummary')"
                  spellcheck="false" rows="2" class="min-h-0 bg-zinc-900 font-mono text-xs" />
              </div>
            </li>
          </ul>
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
        <p v-if="!transfer && message" role="alert" class="break-words text-sm text-red-400">{{ message }}</p>
        <Button v-if="!transfer" :disabled="loading || !destination.trim() || !account.rgb11WriteAllowed"
          class="w-full" @click="prepare">{{ t('rgb11Transfer.prepare') }}</Button>
        <div v-else
          class="sticky bottom-0 space-y-2 border-t border-border bg-background py-3">
          <p v-if="message" role="alert" class="break-words text-sm text-red-400">{{ message }}</p>
          <p v-if="cancelReview" role="status" class="text-sm">{{ t('rgb11Transfer.taskCancelConfirm') }}</p>
          <p v-else-if="receiptsMissing" class="text-xs text-muted-foreground">{{ t('rgb11Transfer.recipientSummaryHelp') }}</p>
          <div class="flex flex-wrap justify-end gap-2">
            <Button variant="outline" class="h-11" @click="handleOpenChange(false)">{{ t('rgb11Transfer.close') }}</Button>
            <template v-if="cancelReview">
              <Button variant="outline" :disabled="loading" class="h-11" @click="cancelReview = false">{{ t('rgb11Transfer.keepTransfer') }}</Button>
              <Button variant="destructive" :disabled="loading" class="h-11" @click="cancel">{{ t('rgb11Transfer.confirmCancel') }}</Button>
            </template>
            <template v-else>
              <Button v-if="canCancelTransfer" variant="outline" :disabled="loading" class="h-11" @click="requestCancel">{{ t('rgb11Transfer.cancelTransfer') }}</Button>
              <Button v-if="transfer.can_confirm" :disabled="loading || receiptsMissing" class="h-11 flex-1 sm:flex-none" @click="confirm">
                {{ t(transfer.needs_receipts ? 'rgb11Invoice.confirmAndBroadcast' : 'common.confirm') }}
              </Button>
            </template>
          </div>
        </div>
      </div>
    </DialogContent>
  </Dialog>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import walletManager from '@/utils/sat20'
import RGB11InvoiceGuide from './RGB11InvoiceGuide.vue'
import { downloadExportFile } from '@/utils/fileSave'
import type { ExportFile, RGBTransferView, RGBTransferFile } from '@/utils/walletViews'
import { assetApprovalLabel } from '@/utils/walletViews'
import { useWalletStore } from '@/store/wallet'
import { useRGB11Store } from '@/store/rgb11'
import { useAccountManagementStore } from '@/store/accountManagement'
import { subscribeWalletIdentity } from '@/lib/identity-boundary'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { useToast } from '@/components/ui/toast-new'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
const props = defineProps<{asset: {key?: string; asset?: string; chain_asset?: string; contract_id?: string; label?: string} | null; resumeTransferId?: string; inflation?: boolean}>()
const emit = defineEmits<{(event: 'completed'): void}>()
const isOpen = defineModel('open', {type: Boolean})
const {t, te} = useI18n()
const { toast } = useToast()
const account = useAccountManagementStore()
const wallet = useWalletStore()
const rgb11 = useRGB11Store()
const mode = ref('address')
const addressMode = computed(() => mode.value === 'address' && !props.inflation)
const fieldId = useId()
const destination = ref('')
const amount = ref('')
const transfer = ref<RGBTransferView | null>(null)
const receipts = ref<string[]>([])
const receiptsMissing = computed(() => !!transfer.value?.needs_receipts &&
  transfer.value.recipients.some((_, index) => !receipts.value[index]?.trim()))
const files = ref<RGBTransferFile[]>([])
const loading = ref(false)
const preparing = ref(false)
const message = ref('')
const saveNotice = ref('')
const uncertain = ref(false)
const cancelReview = ref(false)
const canCancelTransfer = computed(() => !!transfer.value?.can_cancel && transfer.value.status !== 'preview' && !uncertain.value)
const assetKey = () => props.asset?.key || props.asset?.asset || ''
const maxSendAmount = computed(() => {
  const { lock_scope: scope, assets, can_write: canWrite, error_code: errorCode } = rgb11.state
  if (!canWrite || errorCode || rgb11.error || scope.network !== wallet.network ||
      String(scope.wallet_id) !== String(wallet.walletId) || scope.account_index !== Number(wallet.accountIndex)) {
    return undefined
  }
  return assets.find(asset => asset.asset === assetKey())?.available
})
const handleOpenChange = (open: boolean) => {
  if (!open && preparing.value) return
  isOpen.value = open
}
const preventDismiss = (event: Event) => {
  if (preparing.value) event.preventDefault()
}
const displayStatus = (value: string) => {
  if (value === 'preview') return t('rgb11Transfer.preview')
  const key = `walletComponents.states_${value.replace(/-/g, '_')}`
  return te(key) ? t(key) : value
}
const displayWarning = (value: string) => value === 'rgb_address_carrier' ? t('rgb11Transfer.carrierWarning') : value
let generation = 0
const run = async (action: () => void | Promise<void>) => {
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
        ? [{destination: destination.value.trim(), asset: props.asset?.chain_asset || assetKey(), amount: amount.value.trim()}]
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
  if (!transfer.value || receiptsMissing.value) return
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
    if (error?.message === 'RGB recipient receipt does not match the transfer') {
      throw new Error(t('rgb11Transfer.summaryMismatch'))
    }
    throw error || new Error(t('rgb11Transfer.broadcastFailed'))
  }
  confirming.delete(id)
  if (confirmedGeneration === generation && isOpen.value) {
    transfer.value = value; uncertain.value = false
  }
  emit('completed')
})
const requestCancel = () => {
  message.value = ''
  cancelReview.value = true
}
const cancel = () => run(async () => {
  if (!transfer.value || !canCancelTransfer.value) return
  const { id } = transfer.value
  const cancelGeneration = generation
  const [error, value] = await walletManager.cancelRGB11Transfer(id)
  if (error || !value) throw error || new Error(t('rgb11Transfer.taskCancelFailed'))
  if (cancelGeneration === generation && isOpen.value && transfer.value?.id === id) {
    transfer.value = null
    isOpen.value = false
    toast({ variant: 'success', title: t('rgb11Transfer.taskCancelled') })
  }
  emit('completed')
})
const exportTransfer = () => run(async () => {
  if (!transfer.value) return
  const exportGeneration = generation
  const [error, value] = await walletManager.exportRGB11Transfer(transfer.value.id)
  if (error) throw error
  if (exportGeneration === generation && isOpen.value) files.value = value || []
})
const downloadTransferFile = (file: ExportFile) => run(() => {
  saveNotice.value = ''
  const filename = downloadExportFile(file)
  saveNotice.value = t('common.fileDownloadStarted', { filename })
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
  cancelReview.value = false
  if (!open) {
    discardPreview(transfer.value)
    if (transfer.value?.status === 'preview') transfer.value = null
    return
  }
  discardPreview(transfer.value)
  message.value = ''; saveNotice.value = ''; files.value = []; receipts.value = []; transfer.value = null; uncertain.value = false
  destination.value = ''; amount.value = ''
  const firstAccount = wallet.accounts?.find(account => Number(account.index) === 0)
  const root = Number(wallet.accountIndex) === 0 && (!wallet.rootAccountId || firstAccount?.accountId === wallet.rootAccountId)
  mode.value = props.inflation || !root ? 'invoice' : 'address'
  if (props.resumeTransferId) void run(async () => {
    const [error, value] = await walletManager.getRGB11View()
    if (error) throw error
    transfer.value = value?.transfers.find(item => item.id === props.resumeTransferId) || null
    if (!transfer.value) throw new Error(t('rgb11Transfer.taskResumeFailed'))
    receipts.value = transfer.value.recipients.map(() => '')
  })
}, {immediate: true, flush: 'sync'})
</script>
