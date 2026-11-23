<template>
  <Dialog :open="isOpen" @update:open="handleOpenChange">
    <DialogContent class="max-h-[calc(100dvh_-_2rem)] w-[calc(100%_-_2rem)] max-w-md grid-cols-[minmax(0,1fr)] gap-4 overflow-y-auto rounded-xl bg-background"
      @escape-key-down="preventDismiss" @interact-outside="preventDismiss">
      <DialogHeader><DialogTitle>{{ t('rgb11Invoice.title') }}</DialogTitle><DialogDescription class="break-words">{{ asset?.label || asset?.contract_id }}</DialogDescription></DialogHeader>
      <div class="space-y-3">
        <RGB11InvoiceGuide v-if="(receive?.transport || transport) === 'manual'" />
        <Label v-if="requests.length" :for="fieldId + '-saved'">{{ t('rgb11Invoice.savedRequests') }}</Label>
        <select v-if="requests.length" :id="fieldId + '-saved'" :value="receive?.id || ''" :disabled="loading"
          @change="selectRequest(($event.target as HTMLSelectElement).value)" class="w-full rounded bg-zinc-900 p-2">
          <option value="">{{ t('rgb11Transfer.startNewRequest') }}</option>
          <option v-for="item in requests" :key="item.id" :value="item.id">
            {{ t('rgb11Invoice.savedRequest', { amount: item.amount.amount, status: displayStatus(item.status), reference: item.id.slice(-8) }) }}
          </option>
        </select>
        <template v-if="!receive">
          <Label :for="fieldId + '-amount'">{{ t('rgb11Invoice.amount') }}</Label>
          <Input :id="fieldId + '-amount'" v-model="amount" :disabled="loading" inputmode="decimal" />
          <Label :for="fieldId + '-mode'">{{ t('rgb11Invoice.receiveMode') }}</Label>
          <select :id="fieldId + '-mode'" v-model="receiveMode" :disabled="loading" class="w-full rounded bg-zinc-900 p-2">
            <option value="witness">{{ t('rgb11Invoice.witnessMode') }}</option>
            <option value="blind">{{ t('rgb11Invoice.blindMode') }}</option>
          </select>
          <p class="text-xs leading-relaxed text-muted-foreground">
            {{ t(receiveMode === 'witness' ? 'rgb11Invoice.witnessModeHelp' : 'rgb11Invoice.blindModeHelp') }}
          </p>
          <Label :for="fieldId + '-transport'">{{ t('rgb11Invoice.transport') }}</Label>
          <select :id="fieldId + '-transport'" v-model="transport" :disabled="loading" class="w-full rounded bg-zinc-900 p-2">
            <option value="manual">{{ t('rgb11Invoice.outOfBandTransport') }}</option>
            <option value="proxy">{{ t('rgb11Invoice.standardTransport') }}</option>
          </select>
          <p class="text-xs leading-relaxed text-muted-foreground">
            {{ t(transport === 'manual' ? 'rgb11Invoice.outOfBandTransportHelp' : 'rgb11Invoice.standardTransportHelp') }}
          </p>
          <template v-if="transport === 'proxy'">
            <Label :for="fieldId + '-proxy'">{{ t('rgb11Invoice.proxyEndpoint') }}</Label>
            <Input :id="fieldId + '-proxy'" v-model="proxyEndpoint" :disabled="loading" />
          </template>
          <Button :disabled="loading || createPending || !account.rgb11WriteAllowed" @click="generate">{{ t(createPending ? 'rgb11Invoice.generating' : 'rgb11Invoice.generate') }}</Button>
        </template>
        <template v-else>
          <div class="flex flex-wrap justify-between gap-2 text-sm">
            <p role="status">{{ displayStatus(receive.status) }}</p>
            <p>{{ t('rgb11Invoice.amount') }} {{ receive.amount.amount }}</p>
          </div>
          <p v-if="receive.status === 'waiting'" class="text-xs leading-relaxed text-muted-foreground">
            {{ t(receive.transport === 'manual' ? 'rgb11Invoice.shareInvoiceHelp' : 'rgb11Invoice.shareProxyInvoiceHelp') }}
          </p>
          <Label :for="fieldId + '-invoice'">{{ t('rgb11Invoice.invoice') }}</Label>
          <Textarea :id="fieldId + '-invoice'" :model-value="receive.invoice" readonly class="font-mono text-xs" />
          <Button variant="outline" :disabled="!receive.invoice" @click="copyText(receive.invoice, 'invoice')">
            {{ t('rgb11Invoice.copy') }}
          </Button>
          <div v-if="receive.can_import" class="space-y-2 border-t border-border pt-3">
            <p class="text-xs leading-relaxed text-muted-foreground">{{ t('rgb11Invoice.validateFilesHelp') }}</p>
            <Label :for="fieldId + '-package'">{{ t('rgb11Transfer.package') }}</Label>
            <Textarea :id="fieldId + '-package'" v-model="packageText" :disabled="loading" spellcheck="false"
              @input="clearFile" class="font-mono text-xs" />
            <input :id="fieldId + '-file'" type="file" accept=".asc,.rgb,.rgbc,.txt,.json" :disabled="loading"
              class="peer sr-only" @change="selectFile" />
            <label :for="fieldId + '-file'"
              class="flex min-h-10 cursor-pointer items-center justify-center rounded-md border border-input px-3 text-sm peer-focus-visible:ring-1 peer-focus-visible:ring-ring peer-disabled:opacity-50">
              {{ t('rgb11Invoice.selectTransferFile') }}
            </label>
            <p v-if="fileName" class="break-words text-xs text-muted-foreground">{{ fileName }}</p>
            <Button :disabled="loading || (!packageText && !fileData)" @click="prepare">{{ t('rgb11Transfer.prepareReceive') }}</Button>
          </div>
          <div v-if="receive.receipt" class="space-y-2 border-t border-border pt-3">
            <Label :for="fieldId + '-receipt'">{{ t('rgb11Invoice.receipt') }}</Label>
            <p class="text-xs leading-relaxed text-muted-foreground">{{ t('rgb11Invoice.shareReceiptHelp') }}</p>
            <Textarea :id="fieldId + '-receipt'" :model-value="receive.receipt" readonly class="font-mono text-xs" />
            <Button variant="outline" @click="copyText(receive.receipt, 'receipt')">{{ t('rgb11Transfer.copySummary') }}</Button>
          </div>
          <p v-if="receive.status === 'awaiting_confirmation'" class="text-xs leading-relaxed text-muted-foreground">{{ t('rgb11Invoice.autoReceiveHelp') }}</p>
          <p v-else-if="receive.status === 'pending_chain'" class="text-xs leading-relaxed text-muted-foreground">{{ t('rgb11Invoice.waitForBitcoin') }}</p>
          <Button :disabled="loading" variant="outline" @click="refresh">{{ t('rgb11Transfer.checkRequestState') }}</Button>
          <p v-if="receive.error_code" class="text-xs text-red-400">{{ receive.error_code }}</p>
        </template>
        <p v-if="createTimedOut || (createPending && !createWaiting)" role="status" class="text-xs text-amber-400">{{ createNotice }}</p>
        <p v-if="message" role="alert" class="break-all text-xs text-red-400">{{ message }}</p>
      </div>
    </DialogContent>
  </Dialog>
</template>
<script lang="ts">
import { shallowRef } from 'vue'
// The WASM request can outlive a dialog instance; remounting must not resend it.
const invoiceCreationPending = shallowRef(false)
</script>
<script setup lang="ts">
import { computed, onBeforeUnmount, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import walletManager from '@/utils/sat20'
import RGB11InvoiceGuide from './RGB11InvoiceGuide.vue'
import type { RGBReceiveView } from '@/utils/walletViews'
import { useAccountManagementStore } from '@/store/accountManagement'
import { useRGB11Store } from '@/store/rgb11'
import { useWalletStore } from '@/store/wallet'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { useToast } from '@/components/ui/toast-new'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
const props = defineProps<{asset: {key?: string; asset?: string; ticker: string; contract_id?: string; label?: string} | null}>()
const isOpen = defineModel('open', {type: Boolean})
const {t, te, locale} = useI18n()
const fieldId = useId()
const { toast } = useToast()
const account = useAccountManagementStore()
const rgb11 = useRGB11Store()
const wallet = useWalletStore()
const assetKey = computed(() => props.asset?.key || props.asset?.asset || '')
const amount = ref(''); const receiveMode = ref('witness'); const transport = ref('manual'); const proxyEndpoint = ref('')
const receive = ref<RGBReceiveView | null>(null); const requests = ref<RGBReceiveView[]>([])
const packageText = ref(''); const fileData = ref(''); const loading = ref(false); const message = ref('')
const fileName = ref('')
const createPending = invoiceCreationPending
const createWaiting = ref(false); const createTimedOut = ref(false)
const createNotice = computed(() => locale.value.startsWith('zh')
  ? `${createTimedOut.value ? '等待已超过 30 秒。' : ''}发票可能仍在生成。可关闭后稍后重新打开查看已保存发票；关闭不会取消开票，请勿重复生成。`
  : `${createTimedOut.value ? 'The 30-second wait has ended. ' : ''}The invoice may still be generating. You can close and reopen later to check saved invoices. Closing does not cancel creation; do not submit it again.`)
type RequestContext = { generation: number; asset: string }
let generation = 0
let createWaitTimer: ReturnType<typeof setTimeout> | undefined
const isCurrent = (context: RequestContext) => isOpen.value && context.generation === generation && context.asset === assetKey.value
const clearCreateWait = () => {
  if (createWaitTimer !== undefined) clearTimeout(createWaitTimer)
  createWaitTimer = undefined; createWaiting.value = false
}
const handleOpenChange = (open: boolean) => { if (open || !createWaiting.value) isOpen.value = open }
const preventDismiss = (event: Event) => { if (createWaiting.value) event.preventDefault() }
const displayStatus = (value: string) => te(`walletComponents.states_${value}`) ? t(`walletComponents.states_${value}`) : value
const copyText = async (text: string, kind: 'invoice' | 'receipt') => {
  try {
    await navigator.clipboard.writeText(text)
    toast({ variant: 'success', title: t(kind === 'invoice' ? 'rgb11Invoice.copied' : 'rgb11Invoice.receiptCopied') })
  } catch {
    toast({ variant: 'destructive', title: t(kind === 'invoice' ? 'rgb11Invoice.copyFailed' : 'rgb11Invoice.receiptCopyFailed') })
  }
}
const base64 = (data: Uint8Array) => { let text = ''; for (const byte of data) text += String.fromCharCode(byte); return btoa(text) }
const run = async (action: (context: RequestContext) => Promise<void>) => {
  if (loading.value || !isOpen.value) return
  const context = {generation: ++generation, asset: assetKey.value}
  loading.value = true; message.value = ''
  try { await action(context) } catch (error) { if (isCurrent(context)) message.value = error instanceof Error ? error.message : String(error) }
  finally { if (isCurrent(context)) loading.value = false }
}
const clearFile = () => { fileData.value = ''; fileName.value = '' }
const selectRequest = (id: string) => { receive.value = requests.value.find(item => item.id === id) || null; packageText.value = ''; clearFile() }
const updateRequests = (items: RGBReceiveView[]) => {
  requests.value = items.filter(item => item.amount.asset === assetKey.value)
  const selected = receive.value
  if (selected) receive.value = requests.value.find(item => item.id === selected.id) || selected
}
const load = async (context: RequestContext, refresh = false) => {
  const [error, value] = refresh ? await walletManager.refreshRGB11State() : await walletManager.getRGB11View()
  if (!isCurrent(context)) return
  if (error) throw error
  updateRequests(value?.receives || [])
}
const generate = () => {
  if (createPending.value) return
  return run(async context => {
    createPending.value = true; createWaiting.value = true; createTimedOut.value = false
    createWaitTimer = setTimeout(() => {
      if (!isCurrent(context)) return
      generation++; loading.value = false; createTimedOut.value = true; clearCreateWait()
    }, 30_000)
    let result: Awaited<ReturnType<typeof walletManager.createRGB11Invoice>>
    try {
      result = await walletManager.createRGB11Invoice({asset: context.asset, amount: amount.value.trim(), receive_mode: receiveMode.value, transport: transport.value, proxy_endpoint: proxyEndpoint.value.trim()})
    } finally {
      createPending.value = false; clearCreateWait()
    }
    if (!isCurrent(context)) return
    const [error, value] = result
    if (error || !value || value.amount.asset !== context.asset) throw error || new Error(t('rgb11Invoice.generateFailed'))
    receive.value = value; await load(context)
  })
}
const selectFile = async (event: Event) => {
  const context = {generation, asset: assetKey.value}; const requestID = receive.value?.id
  const file = (event.target as HTMLInputElement).files?.[0]
  if (file) {
    const data = await file.arrayBuffer()
    if (!isCurrent(context) || receive.value?.id !== requestID) return
    fileData.value = base64(new Uint8Array(data)); fileName.value = file.name; packageText.value = ''
  }
}
const prepare = () => run(async context => {
  if (!receive.value) return
  const [error, value] = await walletManager.prepareRGB11Consignment(receive.value.id, packageText.value ? base64(new TextEncoder().encode(packageText.value)) : fileData.value)
  if (!isCurrent(context)) return
  if (error || !value || value.amount.asset !== context.asset) throw error || new Error(t('rgb11Transfer.prepareFailed'))
  receive.value = value
})
const refresh = () => run(context => load(context, true))
watch(() => rgb11.state, ({ lock_scope: scope, receives }) => {
  if (!isOpen.value || loading.value || scope.network !== wallet.network ||
      String(scope.wallet_id) !== String(wallet.walletId) || scope.account_index !== Number(wallet.accountIndex)) return
  updateRequests(receives)
})
onBeforeUnmount(() => { generation++; clearCreateWait() })
watch([isOpen, assetKey], ([open]) => {
  generation++; clearCreateWait(); loading.value = false; createTimedOut.value = false
  receive.value = null; requests.value = []; packageText.value = ''; clearFile(); message.value = ''
  if (open) void run(context => load(context))
}, {immediate: true})
</script>
