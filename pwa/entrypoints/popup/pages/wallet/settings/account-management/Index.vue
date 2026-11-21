<template>
  <LayoutScroll>
    <div class="mx-auto max-w-2xl space-y-5 p-4">
      <div class="flex items-center gap-2"><Button variant="ghost" @click="router.back()">{{ t('common.back') }}</Button><h1 class="text-xl font-semibold">{{ t('accountManagement.ui.managementTitle') }}</h1></div>
      <section v-if="state" class="space-y-2 rounded border p-3">
        <p>{{ t('accountManagement.ui.accountStatus', {value: t(`accountManagement.accountState.${state.state}`)}) }}</p>
        <p>{{ t('accountManagement.ui.backupState', {value: t(`accountManagement.backupState.${state.backup_state}`)}) }}</p>
        <p v-if="state.storage">{{ t(state.storage.option.id === 'paid' ? 'accountManagement.ui.paidStorage' : 'accountManagement.ui.temporaryStorage') }}</p>
        <p v-if="state.last_rehearsal_at">{{ t('accountManagement.ui.lastRehearsal', {value: new Date(state.last_rehearsal_at).toLocaleString()}) }}</p>
        <p v-if="state.error_code" role="alert">{{ state.error_code }}</p>
        <p v-for="module in state.modules.filter(item => item.error_code)" :key="module.name">{{ module.name }}: {{ module.error_code }}</p>
        <Button v-if="accountState.canRetryBackup" variant="outline" :disabled="busy" @click="run(async () => { await accountState.retryNow() })">{{ t('accountManagement.readiness.retryNow') }}</Button>
        <Button v-if="state.backup_risk === 'notConfigured' && state.discovery_status === 'active-waiting-backup'" variant="outline" @click="router.push('/restore-account')">{{ t('accountManagement.ui.restoreExisting') }}</Button>
        <template v-if="state.locator">
          <Textarea :model-value="state.locator" readonly />
          <Button variant="outline" @click="copy(state.locator)">{{ t('accountManagement.ui.copyRecoveryCode') }}</Button>
          <Button variant="outline" @click="router.push('/restore-account?mode=rehearsal')">{{ t('accountManagement.ui.rehearsalTitle') }}</Button>
        </template>
      </section>
      <section v-if="state?.autopay && state.backup_risk !== 'notConfigured'" class="space-y-2 rounded border p-3">
        <h2>{{ t('accountManagement.dkvsPaymentDetails') }}</h2>
        <p>{{ state.autopay.status }} · {{ state.autopay.balance }}</p>
        <p v-if="state.autopay.error_code" class="text-sm text-destructive" role="alert">{{ accountDisplayMessage(t, state.autopay.error_code) }}</p>
        <p v-if="state.autopay.expiry_height">{{ t('accountManagement.dkvsExpiryEstimate', {height: state.autopay.expiry_height}) }}</p>
        <p v-if="state.autopay.tx_id" class="break-all font-mono text-xs">{{ state.autopay.tx_id }}</p>
        <Button v-if="state.autopay.can_fund && state.autopay.quote" :disabled="busy" @click="askFunding">{{ t('accountManagement.dkvsPay') }}</Button>
      </section>
      <p v-if="accountState.storagePhase === 'activate' && accountState.storageOffers && !accountState.canActivate" role="status">{{ t('accountManagement.ui.temporaryUnavailable') }}</p>
      <p v-if="state?.state === 'local' && state.discovery_status === 'unknown'" role="alert">{{ t('accountManagement.ui.mnemonicRootRequired') }}</p>
      <div v-if="accountState.storageReadFailed" class="space-y-2">
        <p role="status">{{ t('accountManagement.ui.configReadFailed') }}</p>
        <Button variant="outline" :disabled="busy" @click="run(() => accountState.refreshStorageOffers())">{{ t('common.refresh') }}</Button>
      </div>
      <section v-if="accountState.canActivate || state?.state === 'ready' || summary || creation" class="space-y-3 rounded border p-3">
        <h2 class="font-medium">{{ t('accountManagement.ui.createRecovery') }}</h2>
        <p v-if="accountState.canActivate && !summary && !creation">{{ t('accountManagement.ui.activateCacheHint') }}</p>
        <template v-if="!summary && !creation">
          <Button :disabled="busy" @click="preflight(state?.state === 'ready' && state.storage?.option.id === 'paid' ? 'paid' : 'temporary')">{{ t('accountManagement.ui.preflight') }}</Button>
          <Button v-if="accountState.canUpgrade" variant="outline" :disabled="busy" @click="preflight('paid')">{{ t('accountManagement.ui.upgradeStorage') }}</Button>
        </template>
        <div v-if="summary" class="space-y-1">
          <p v-for="wallet in summary.wallets" :key="wallet.name">{{ wallet.name }} · {{ t('accountManagement.ui.subaccountCount', {value: wallet.account_count}) }}</p>
        </div>
        <template v-if="summary && !creation">
          <template v-if="setupMode === 'paid'">
            <Label>{{ t('accountManagement.ui.recordCount') }}</Label><Input v-model.number="recordCount" type="number" min="1" :disabled="busy || storage?.status === 'ready'" />
          </template>
          <Button variant="outline" :disabled="busy || storage?.status === 'ready'" @click="loadOptions">{{ t('common.refresh') }}</Button>
          <div v-for="option in options" :key="option.id" class="rounded border p-3 space-y-2">
            <p>{{ t(option.id === 'paid' ? 'accountManagement.ui.paidStorage' : 'accountManagement.ui.temporaryStorage') }}</p>
            <p v-if="option.id === 'temporary' && option.ttl_blocks">{{ t('accountManagement.ui.retentionBlocks', {value: option.ttl_blocks}) }}</p>
            <p v-if="option.payment">{{ option.payment.amount.amount }} {{ option.payment.amount.asset }} · {{ option.payment.funding_blocks }} blocks</p>
            <p v-if="option.annual_cost">{{ t('accountManagement.ui.annualCost', {value: option.annual_cost}) }}</p>
            <p v-for="warning in option.warning_codes" :key="warning" class="text-xs text-amber-500">{{ storageWarning(warning) }}</p>
            <p v-if="option.unavailable_code">{{ option.unavailable_code }}</p>
            <Button :disabled="busy || !option.available || storage?.status === 'ready'" @click="selectStorage(option)">{{ t('common.confirm') }}</Button>
          </div>
          <p v-if="storage">{{ storage.status }} <span class="break-all">{{ storage.tx_id }}</span></p>
          <Button v-if="storage?.can_cancel" variant="outline" :disabled="busy" @click="cancelStorage">{{ t('common.cancel') }}</Button>
          <template v-if="storage?.status === 'ready'">
            <select v-model="mode" :disabled="busy" class="w-full rounded bg-muted p-2"><option value="2of2">{{ t('accountManagement.ui.modeSecure') }}</option></select>
            <div v-for="(item, index) in questions" :key="item.question.id" class="space-y-2">
              <Input v-model="item.question.prompt" :aria-label="String(index + 1)" />
              <Input v-model="item.answer" type="password" autocomplete="off" :placeholder="t('accountManagement.ui.answer')" />
              <Input v-model="item.confirmation" type="password" autocomplete="off" :placeholder="t('accountManagement.ui.repeatAnswer')" />
            </div>
            <Button :disabled="busy" @click="createRecovery">{{ t('accountManagement.ui.createRecovery') }}</Button>
          </template>
        </template>
        <template v-if="creation">
          <div v-for="file in creation.files" :key="file.name" class="space-y-2 rounded border p-2">
            <p>{{ file.name }}</p><Button variant="outline" @click="saveExportFile(file)">{{ t('common.download') }}</Button>
          </div>
          <p class="text-sm text-muted-foreground">{{ t('accountManagement.ui.keepRecoveryFileSafe') }}</p>
          <Label>{{ t('accountManagement.ui.recoveryFile') }}</Label>
          <Textarea v-model="recoveryFile" spellcheck="false" autocomplete="off" autocapitalize="off"
            :placeholder="t('accountManagement.ui.pasteRecoveryFile')" />
          <div v-for="(question, index) in creation.questions" :key="question.id"><Label>{{ question.prompt }}</Label><Input v-model="answers[index]" type="password" autocomplete="off" /></div>
          <Button :disabled="busy" @click="activate">{{ t('accountManagement.ui.rehearsalTitle') }}</Button>
          <Button variant="outline" :disabled="busy" @click="abort">{{ t('common.cancel') }}</Button>
        </template>
      </section>
      <p v-if="error" class="break-all text-sm text-destructive" role="alert">{{ error }}</p>
      <Dialog :open="!!payment" @update:open="value => { if (!value) cancelPayment() }">
        <DialogContent><DialogHeader><DialogTitle>{{ t('accountManagement.dkvsPay') }}</DialogTitle><DialogDescription>{{ t('accountManagement.ui.paidWarning') }}</DialogDescription></DialogHeader>
          <dl v-if="payment" class="space-y-2 break-all text-sm">
            <dt>{{ t('accountManagement.ui.paymentSource') }}</dt><dd>{{ payment.source_wallet }} / {{ payment.source_account }} / {{ payment.source_address }}</dd>
            <dt>{{ t('common.network') }}</dt><dd>{{ payment.network }}</dd>
            <dt>{{ t('accountManagement.ui.paymentContract') }}</dt><dd>{{ payment.contract }}</dd>
            <dt>{{ t('common.amount') }}</dt><dd>{{ payment.amount.amount }} {{ payment.amount.asset }}</dd>
            <dt>{{ t('accountManagement.ui.fundingBlocks') }}</dt><dd>{{ payment.funding_blocks }}</dd>
            <dt>{{ t('accountManagement.ui.ratePerBlock') }}</dt><dd>{{ payment.rate_per_block }}</dd>
            <dt>{{ t('accountManagement.ui.moduleLimit') }}</dt><dd>{{ payment.module_limit }}</dd>
            <dd v-if="payment.tx_id">{{ payment.tx_id }}</dd>
          </dl>
          <Button :disabled="busy" @click="confirmPayment">{{ t('common.confirm') }}</Button>
          <Button variant="outline" :disabled="busy" @click="cancelPayment">{{ t('common.cancel') }}</Button>
        </DialogContent>
      </Dialog>
    </div>
  </LayoutScroll>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useClipboard, useDocumentVisibility } from '@vueuse/core'
import LayoutScroll from '@/components/layout/LayoutScroll.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { useWalletStore } from '@/store/wallet'
import { useAccountManagementStore } from '@/store/accountManagement'
import { withWalletPassword } from '@/lib/walletPasswordPrompt'
import accountSDK, { accountDisplayMessage, saveExportFile } from '@/utils/accountManagement'
import type { PaymentQuote, StorageOption, StorageView, RecoverySummary, RecoverySetupView, QuestionInput } from '@/utils/walletViews'
const {t} = useI18n(); const {copy} = useClipboard(); const router = useRouter()
const wallet = useWalletStore(); const accountState = useAccountManagementStore()
const state = computed(() => accountState.status)
const autopay = computed(() => state.value?.autopay)
const visibility = useDocumentVisibility()
const pendingFundingTx = ref('')
const visibleFundingTx = computed(() => accountState.sessionReady && visibility.value === 'visible'
  ? pendingFundingTx.value : '')
const busy = ref(false); const error = ref(''); let disposed = false
const summary = ref<RecoverySummary | null>(null); const storage = ref<StorageView | null>(null)
const options = ref<StorageOption[]>([]); const recordCount = ref(100)
const setupMode = ref<'temporary' | 'paid'>('temporary')
const mode = ref('2of2'); const creation = ref<RecoverySetupView | null>(null)
const recoveryFile = ref(''); const answers = ref<string[]>([])
const questions = ref<QuestionInput[]>(['bookQuestion', 'phraseQuestion', 'noteQuestion'].map((key, index) => ({question: {id: `question-${index + 1}`, prompt: t(`accountManagement.ui.${key}`), case_sensitive: false, ignore_punctuation: true}, answer: '', confirmation: ''})))
const payment = ref<PaymentQuote | null>(null); let paymentAction: (() => Promise<void>) | undefined
const storageWarning = (code: string) => t(`accountManagement.ui.${({temporary_storage: 'temporaryWarning', same_node_required: 'sameNodeWarning', continuous_payment_required: 'paidWarning'} as Record<string,string>)[code] || code}`)
const run = async (action: () => Promise<void>) => {
  if (busy.value) return
  busy.value = true; error.value = ''
  try { await action() } catch (cause) { if (!disposed) error.value = accountDisplayMessage(t, cause instanceof Error ? cause.message : String(cause)) }
  finally { busy.value = false }
}
const withPassword = (action: (password: string) => Promise<void>) => run(async () => { await withWalletPassword(action) })
const walletIDs = () => wallet.wallets.map(item => Number(item.id))
const readOptions = async () => {
  options.value = []
  const available = setupMode.value === 'paid'
    ? await accountSDK.getStorageOptions(recordCount.value)
    : await accountSDK.getStorageAvailability()
  options.value = available.filter(option => option.id === setupMode.value)
  if (setupMode.value === 'paid' && options.value.length === 0) {
    await accountState.refreshStorageOffers()
    throw new Error(t('accountManagement.ui.paidUnavailable'))
  }
}
const preflight = (selected: 'temporary' | 'paid') => withPassword(async password => {
  setupMode.value = selected
  const backup = await accountSDK.preflightRecovery(password, walletIDs())
  await readOptions()
  const existing = (await accountSDK.getAccountView()).storage
  storage.value = existing?.can_cancel && existing.option.id === selected ? existing : null
  summary.value = backup
})
const loadOptions = () => run(readOptions)
const requestPayment = (quote: PaymentQuote | null, action: () => Promise<void>) => {
  if (quote) { payment.value = quote; paymentAction = action } else void run(action)
}
const cancelPayment = () => { payment.value = null; paymentAction = undefined }
const confirmPayment = () => run(async () => { const action = paymentAction; cancelPayment(); if (action) await action(); await accountState.refresh() })
const selectStorage = (option: StorageOption) => requestPayment(option.payment, async () => {
  try {
    storage.value = await accountSDK.confirmStorage(option)
  } catch (cause) {
    if ((cause as {code?: string})?.code === 'STORAGE_TTL_CHANGED') {
      await readOptions()
      throw new Error(t('accountManagement.ui.retentionChanged'))
    }
    throw cause
  }
  await accountState.refresh()
})
const cancelStorage = () => run(async () => {
  await accountSDK.cancelStorageSetup()
  storage.value = null
  summary.value = null
  await accountState.refresh()
})
const askFunding = () => {
  const quote = autopay.value?.quote
  if (!quote) return
  requestPayment(quote, async () => {
    const result = await accountSDK.fundAutopay(quote)
    if (!disposed && result.status === 'pending') pendingFundingTx.value = result.tx_id
  })
}

watch(() => accountState.identityKey, () => {
  pendingFundingTx.value = ''
  cancelPayment()
}, { flush: 'sync' })
watch(autopay, view => {
  if (view?.status === 'pending') pendingFundingTx.value = view.tx_id
}, { immediate: true })

watch(visibleFundingTx, (txID, _previous, onCleanup) => {
  if (!txID) return
  let timer: number
  let active = true
  onCleanup(() => {
    active = false
    window.clearTimeout(timer)
  })
  const current = () => active && visibleFundingTx.value === txID
  const settledFunding = () => {
    const view = autopay.value
    if (view?.tx_id === txID && (view.status === 'ready' || view.status === 'needs_funding')) return view
  }
  const poll = async () => {
    if (!current()) return
    if (!accountState.loading) {
      const settled = settledFunding()
      if (!settled?.error_code) await accountState.refresh()
      if (!current()) return
      const updated = settledFunding()
      // The SDK reads the balance before checking the receipt. Read again after
      // confirmation so those two queries cannot leave an old balance on screen.
      if (updated?.error_code || (settled && updated && !accountState.loading &&
          (updated.status === 'needs_funding' || updated.balance !== ''))) {
        pendingFundingTx.value = ''
        return
      }
    }
    timer = window.setTimeout(poll, 3000)
  }
  timer = window.setTimeout(poll, 3000)
}, { immediate: true })

const createRecovery = () => withPassword(async password => {
  const value = await accountSDK.createRecovery({password, wallet_ids: walletIDs(), mode: mode.value, questions: questions.value, guardian_contact: ''})
  if (disposed) { await accountSDK.abortRecovery(value.session_id); return }
  creation.value = value; answers.value = value.questions.map(() => '')
  questions.value.forEach(item => { item.answer = ''; item.confirmation = '' })
})
const clearInputs = () => { creation.value = null; recoveryFile.value = ''; answers.value = []; }
const activate = () => withPassword(async password => {
  if (!creation.value) return
  await accountSDK.activateRecovery(creation.value.session_id, {password, recovery_file: recoveryFile.value, factors: {answers: creation.value.questions.map((item,index) => ({question_id: item.id, text: answers.value[index] || ''})).filter(item => item.text), guardian_response: ''}})
  clearInputs(); summary.value = null; storage.value = null; await accountState.refresh()
})
const abort = () => run(async () => { if (creation.value) await accountSDK.abortRecovery(creation.value.session_id); clearInputs() })
onMounted(() => { void run(() => accountState.refresh()) })
watch(() => accountState.storagePhase, () => { void accountState.refreshStorageOffers() }, {immediate: true})
onBeforeUnmount(() => { disposed = true; cancelPayment(); if (creation.value) void accountSDK.abortRecovery(creation.value.session_id).catch(() => undefined); clearInputs() })
</script>
