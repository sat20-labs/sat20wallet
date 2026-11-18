<template>
  <Dialog :open="isOpen" @update:open="handleOpenChange">
    <DialogContent class="max-h-[90vh] w-[380px] overflow-y-auto rounded-lg bg-black"
      @escape-key-down="preventDismiss" @interact-outside="preventDismiss">
      <DialogHeader>
        <DialogTitle>{{ t(inflation ? 'rgb11Transfer.inflate' : 'rgb11Transfer.sendTitle') }}</DialogTitle>
        <DialogDescription class="break-all font-mono text-xs">{{ asset?.contract_id }}</DialogDescription>
      </DialogHeader>
      <div class="space-y-3">
        <template v-if="!transfer">
          <select v-if="!inflation" v-model="mode" :disabled="loading" class="w-full rounded bg-zinc-900 p-2">
            <option value="address">{{ t('rgb11Transfer.addressMode') }}</option>
            <option value="invoice">{{ t('rgb11Transfer.invoiceMode') }}</option>
          </select>
          <Label>{{ t(mode === 'address' && !inflation ? 'rgb11Transfer.receiverAddress' : 'rgb11Transfer.invoice') }}</Label>
          <Textarea v-model="destination" :disabled="loading" spellcheck="false" class="min-h-24 bg-zinc-900 font-mono text-xs" />
          <template v-if="mode === 'address' && !inflation">
            <Label>{{ t('rgb11Invoice.amount') }}</Label>
            <Input v-model="amount" :disabled="loading" inputmode="decimal" />
          </template>
          <p v-else class="text-xs text-muted-foreground">{{ t('rgb11Transfer.batchHint') }}</p>
          <Button :disabled="loading || !destination.trim() || !account.rgb11WriteAllowed" class="w-full" @click="prepare">{{ t('rgb11Transfer.prepare') }}</Button>
        </template>
        <template v-else>
          <p v-if="transfer.status !== 'preview' || uncertain" class="break-all font-mono text-xs">{{ transfer.id }}</p>
          <p>{{ uncertain ? t('rgb11Transfer.confirmUnverified') : displayStatus(transfer.status) }}</p>
          <p v-for="warning in transfer.warning_codes" :key="warning" class="text-xs text-amber-400">{{ displayWarning(warning) }}</p>
          <div v-for="(recipient, index) in transfer.recipients" :key="index" class="space-y-2 rounded border p-2">
            <p class="break-all text-xs">{{ recipient.destination }}</p>
            <p>{{ recipient.amount.amount }} · {{ recipient.amount.asset }}</p>
            <Textarea v-if="transfer.needs_receipts" v-model="receipts[index]" :disabled="loading" :placeholder="t('rgb11Transfer.recipientSummary', {number: index + 1})" class="font-mono text-xs" />
          </div>
          <p>{{ t('common.fee') }}: {{ transfer.fee_sats }} sats</p>
          <p v-if="transfer.tx_id && transfer.status !== 'preview'" class="break-all font-mono text-xs">{{ transfer.tx_id }}</p>
          <Button v-if="transfer.can_export" variant="outline" :disabled="loading" @click="exportTransfer">{{ t('rgb11Transfer.downloadConsignment') }}</Button>
          <div v-for="(item, index) in files" :key="index">
            <Button variant="outline" @click="saveExportFile(item.file)">{{ item.recipient_index + 1 }} · {{ item.file.name }}</Button>
          </div>
          <Button v-if="transfer.can_confirm" :disabled="loading" class="w-full" @click="confirm">{{ t('common.confirm') }}</Button>
          <Button v-if="transfer.can_cancel && !uncertain" :disabled="loading" variant="outline" @click="cancel">{{ t('common.cancel') }}</Button>
        </template>
        <p v-if="message" role="alert" class="break-all text-xs text-red-400">{{ message }}</p>
      </div>
    </DialogContent>
  </Dialog>
</template>
<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
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
const props = defineProps<{asset: {key?: string; asset?: string; contract_id?: string} | null; resumeTransferId?: string; inflation?: boolean}>()
const emit = defineEmits<{(event: 'completed'): void}>()
const isOpen = defineModel('open', {type: Boolean})
const {t, te} = useI18n()
const account = useAccountManagementStore()
const wallet = useWalletStore()
const mode = ref('address')
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
