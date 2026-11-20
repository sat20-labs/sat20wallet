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
        <template v-if="state.locator">
          <Textarea :model-value="state.locator" readonly />
          <Button variant="outline" @click="copy(state.locator)">{{ t('accountManagement.ui.copyRecoveryCode') }}</Button>
          <Button variant="outline" @click="router.push('/restore-account?mode=rehearsal')">{{ t('accountManagement.ui.rehearsalTitle') }}</Button>
        </template>
      </section>
      <section v-if="state?.autopay" class="space-y-2 rounded border p-3">
        <h2>{{ t('accountManagement.dkvsPaymentDetails') }}</h2>
        <p>{{ state.autopay.status }} · {{ state.autopay.balance }}</p>
        <p v-if="state.autopay.expiry_height">{{ t('accountManagement.dkvsExpiryEstimate', {height: state.autopay.expiry_height}) }}</p>
        <p v-if="state.autopay.tx_id" class="break-all font-mono text-xs">{{ state.autopay.tx_id }}</p>
        <Button v-if="state.autopay.can_fund && state.autopay.quote" :disabled="busy" @click="askFunding">{{ t('accountManagement.dkvsPay') }}</Button>
      </section>
      <section class="space-y-3 rounded border p-3">
        <h2 class="font-medium">{{ t('accountManagement.ui.createRecovery') }}</h2>
        <Button v-if="!summary && !creation" :disabled="busy" @click="preflight">{{ t('accountManagement.ui.preflight') }}</Button>
        <div v-if="summary" class="space-y-1">
          <p v-for="wallet in summary.wallets" :key="wallet.name">{{ wallet.name }} · {{ t('accountManagement.ui.subaccountCount', {value: wallet.account_count}) }}</p>
        </div>
        <template v-if="summary && !creation">
          <Label>{{ t('accountManagement.ui.recordCount') }}</Label><Input v-model.number="recordCount" type="number" min="1" :disabled="busy || storage?.status === 'ready'" />
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
            <Button variant="outline" @click="copy(exportFileText(file))">{{ t('common.copy') }}</Button>
          </div>
          <Label>{{ t('accountManagement.ui.recoveryCode') }}</Label><Textarea :model-value="creation.locator" readonly />
          <Button variant="outline" @click="copy(creation.locator)">{{ t('common.copy') }}</Button>
          <Label>{{ t('accountManagement.ui.pasteLocator') }}</Label><Textarea v-model="savedLocator" />
          <div v-for="(question, index) in creation.questions" :key="question.id"><Label>{{ question.prompt }}</Label><Input v-model="answers[index]" type="password" autocomplete="off" /></div>
          <Textarea v-model="userShare" :placeholder="t('accountManagement.ui.pasteUserShare')" />
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
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useClipboard } from '@vueuse/core'
import LayoutScroll from '@/components/layout/LayoutScroll.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { useWalletStore } from '@/store/wallet'
import { useAccountManagementStore } from '@/store/accountManagement'
import { withWalletPassword } from '@/lib/walletPasswordPrompt'
import accountSDK, { accountDisplayMessage, exportFileText, saveExportFile } from '@/utils/accountManagement'
import type { PaymentQuote, StorageOption, StorageView, RecoverySummary, RecoverySetupView, QuestionInput } from '@/utils/walletViews'
const {t} = useI18n(); const {copy} = useClipboard(); const router = useRouter()
const wallet = useWalletStore(); const accountState = useAccountManagementStore()
const state = computed(() => accountState.status)
const busy = ref(false); const error = ref(''); let disposed = false
const summary = ref<RecoverySummary | null>(null); const storage = ref<StorageView | null>(null)
const options = ref<StorageOption[]>([]); const recordCount = ref(100)
const mode = ref('2of2'); const creation = ref<RecoverySetupView | null>(null)
const savedLocator = ref(''); const answers = ref<string[]>([])
const userShare = ref('')
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
const preflight = () => withPassword(async password => { summary.value = await accountSDK.preflightRecovery(password, walletIDs()); options.value = await accountSDK.getStorageOptions(recordCount.value); storage.value = (await accountSDK.getAccountView()).storage; if (!storage.value?.can_cancel) storage.value = null })
const loadOptions = () => run(async () => { options.value = await accountSDK.getStorageOptions(recordCount.value) })
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
      options.value = []
      options.value = await accountSDK.getStorageOptions(recordCount.value)
      throw new Error(t('accountManagement.ui.retentionChanged'))
    }
    throw cause
  }
  await accountState.refresh()
})
const cancelStorage = () => run(async () => { await accountSDK.cancelStorageSetup(); storage.value = null })
const askFunding = () => { const quote = state.value?.autopay?.quote; if (quote) requestPayment(quote, async () => { await accountSDK.fundAutopay(quote) }) }
const createRecovery = () => withPassword(async password => {
  const value = await accountSDK.createRecovery({password, wallet_ids: walletIDs(), mode: mode.value, questions: questions.value, guardian_contact: ''})
  if (disposed) { await accountSDK.abortRecovery(value.session_id); return }
  creation.value = value; answers.value = value.questions.map(() => '')
  questions.value.forEach(item => { item.answer = ''; item.confirmation = '' })
})
const clearInputs = () => { creation.value = null; savedLocator.value = ''; answers.value = []; userShare.value = ''; }
const activate = () => withPassword(async password => {
  if (!creation.value) return
  await accountSDK.activateRecovery(creation.value.session_id, {password, saved_locator: savedLocator.value, factors: {answers: creation.value.questions.map((item,index) => ({question_id: item.id, text: answers.value[index] || ''})).filter(item => item.text), user_share: userShare.value, guardian_response: ''}})
  clearInputs(); summary.value = null; storage.value = null; await accountState.refresh()
})
const abort = () => run(async () => { if (creation.value) await accountSDK.abortRecovery(creation.value.session_id); clearInputs() })
onMounted(() => { void run(() => accountState.refresh()) })
onBeforeUnmount(() => { disposed = true; cancelPayment(); if (creation.value) void accountSDK.abortRecovery(creation.value.session_id).catch(() => undefined); clearInputs() })
</script>
