<template>
  <LayoutSecond :title="t('accountManagement.ui.managementTitle')" hide-nav>
    <div class="min-w-0 space-y-4">
      <AccountReadinessBanner v-if="!state" />
      <section v-if="startSetup" class="space-y-4 rounded-xl border border-primary/30 bg-card p-3 sm:p-4">
        <div class="flex items-start gap-3">
          <div class="flex size-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
            <CloudUpload class="size-5" aria-hidden="true" />
          </div>
          <div class="min-w-0 space-y-1">
            <h2 class="text-base font-semibold">{{ t('accountManagement.ui.backupSetupTitle') }}</h2>
            <p class="text-sm leading-relaxed text-muted-foreground">{{ t('accountManagement.ui.backupSetupHint') }}</p>
          </div>
        </div>
        <div class="space-y-2">
          <Button class="w-full sm:w-auto" :disabled="busy" @click="preflight('temporary')">
            {{ t('accountManagement.ui.startBackupSetup') }}
            <ArrowRight class="size-4" aria-hidden="true" />
          </Button>
          <p class="text-sm leading-relaxed text-muted-foreground">{{ t('accountManagement.ui.recoverySetupHint') }}</p>
          <p v-if="error" class="break-words text-sm text-destructive" role="alert">{{ error }}</p>
        </div>
      </section>
      <section v-if="state" class="space-y-4 rounded-xl border border-border bg-card p-3 text-sm sm:p-4">
        <div class="flex items-start gap-3">
          <div class="flex size-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
            <ShieldCheck v-if="state.backup_state === 'backed_up'" class="size-5" aria-hidden="true" />
            <Shield v-else class="size-5" aria-hidden="true" />
          </div>
          <div class="min-w-0 space-y-1">
            <h2 class="text-base font-semibold">{{ t('accountManagement.ui.backupOverview') }}</h2>
            <p v-if="localOnly" class="leading-relaxed text-muted-foreground">{{ t('accountManagement.ui.localWalletHint') }}</p>
            <p v-else-if="state.backup_state === 'backed_up'" class="leading-relaxed text-muted-foreground">{{ t('accountManagement.ui.backupConfirmedHint') }}</p>
            <p v-if="state.backup_state === 'backed_up' && !state.locator" class="leading-relaxed text-muted-foreground">{{ t('accountManagement.ui.rootRestoreHint') }}</p>
          </div>
        </div>
        <dl class="grid grid-cols-1 gap-px overflow-hidden rounded-lg bg-border min-[360px]:grid-cols-2">
          <div class="min-w-0 space-y-1 bg-muted px-3 py-2.5">
            <dt class="text-xs text-muted-foreground">{{ t('accountManagement.ui.accountStatusLabel') }}</dt>
            <dd class="break-words font-medium">{{ t(`accountManagement.accountState.${state.state}`) }}</dd>
          </div>
          <div class="min-w-0 space-y-1 bg-muted px-3 py-2.5">
            <dt class="text-xs text-muted-foreground">{{ t('accountManagement.ui.backupStatusLabel') }}</dt>
            <dd class="break-words font-medium">{{ t(`accountManagement.backupState.${state.backup_state}`) }}</dd>
          </div>
          <div v-if="state.backup_risk !== 'notConfigured' && state.storage" class="space-y-1 bg-muted px-3 py-2.5 min-[360px]:col-span-2">
            <dt class="text-xs text-muted-foreground">{{ t('accountManagement.ui.backupStorageLabel') }}</dt>
            <dd class="break-words font-medium">{{ t(state.storage.option.id === 'paid' ? 'accountManagement.ui.paidStorage' : 'accountManagement.ui.temporaryStorage') }}</dd>
          </div>
        </dl>
        <AccountReadinessBanner v-if="!localOnly" />
        <details v-if="state.error_code || accountState.moduleIssues.length" class="rounded-md bg-muted/50 px-3 py-2 text-xs">
          <summary class="cursor-pointer font-medium">{{ t('accountManagement.ui.syncDetails') }}</summary>
          <div class="mt-2 space-y-1 break-all font-mono text-muted-foreground">
            <p v-if="state.error_code">{{ state.error_code }}</p>
            <p v-for="module in accountState.moduleIssues" :key="module.name">{{ module.name }}: {{ module.error_code }}</p>
          </div>
        </details>
        <p v-if="waitingForBackup" class="leading-relaxed text-muted-foreground">{{ t('accountManagement.ui.waitingForBackup') }}</p>
        <Button v-if="accountState.canRetryBackup && !accountState.shouldShowBanner" size="sm" variant="outline" :disabled="busy || accountState.retrying"
          @click="run(() => accountState.retryNow())">
          {{ accountState.retrying ? t('accountManagement.readiness.retrying') : t('accountManagement.readiness.retryNow') }}
        </Button>
      </section>
      <section v-if="state?.locator" class="space-y-3 rounded-xl border border-border bg-card p-3 sm:p-4">
        <div class="space-y-1">
          <h2 class="text-base font-semibold">{{ t('accountManagement.ui.recoveryCode') }}</h2>
          <p class="text-sm leading-relaxed text-muted-foreground">{{ t('accountManagement.ui.recoveryCodeHint') }}</p>
          <p v-if="state.storage?.option.id === 'temporary'" class="text-sm leading-relaxed text-muted-foreground">{{ t('accountManagement.ui.temporaryRecoveryHint') }}</p>
        </div>
        <p v-if="state.last_rehearsal_at" class="text-sm text-muted-foreground">
          {{ t('accountManagement.ui.lastRehearsal', {value: new Date(state.last_rehearsal_at).toLocaleString()}) }}
        </p>
        <div class="flex flex-col gap-2 sm:flex-row sm:flex-wrap">
          <Button size="sm" variant="outline" @click="copy(state.locator)">{{ t('accountManagement.ui.copyRecoveryCode') }}</Button>
          <Button size="sm" variant="outline" @click="router.push('/restore-account?mode=rehearsal')">{{ t('accountManagement.ui.rehearsalTitle') }}</Button>
        </div>
        <p v-if="copied" class="text-xs text-muted-foreground" role="status">{{ t('accountManagement.ui.recoveryCodeCopied') }}</p>
      </section>
      <section v-if="state?.autopay && state.backup_risk !== 'notConfigured'" class="space-y-2 rounded-xl border border-border bg-card p-3 sm:p-4">
        <h2>{{ t('accountManagement.dkvsPaymentDetails') }}</h2>
        <p>{{ state.autopay.status }} · {{ state.autopay.balance }}</p>
        <p v-if="state.autopay.error_code" class="text-sm text-destructive" role="alert">{{ accountDisplayMessage(t, state.autopay.error_code) }}</p>
        <p v-if="state.autopay.expiry_height">{{ t('accountManagement.dkvsExpiryEstimate', {height: state.autopay.expiry_height}) }}</p>
        <p v-if="state.autopay.tx_id" class="break-all font-mono text-xs">{{ state.autopay.tx_id }}</p>
        <Button v-if="state.autopay.can_fund && state.autopay.quote" :disabled="busy" @click="askFunding">{{ t('accountManagement.dkvsPay') }}</Button>
      </section>
      <section v-if="(accountState.storagePhase === 'activate' && !accountState.canActivate) || accountState.storageReadFailed"
        class="space-y-3 rounded-xl border border-border bg-card p-3 sm:p-4">
        <div class="flex items-start gap-3">
          <div class="flex size-10 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground">
            <Cloud aria-hidden="true" class="size-5" />
          </div>
          <div class="min-w-0 space-y-1" role="status">
            <h2 class="text-base font-semibold">{{ t(storageNotice.title) }}</h2>
            <p v-if="storageNotice.body" class="text-sm leading-relaxed text-muted-foreground">{{ t(storageNotice.body) }}</p>
          </div>
        </div>
        <Button v-if="accountState.storageOffers || accountState.storageReadFailed" size="sm" variant="outline" :disabled="busy"
          @click="run(() => accountState.refreshStorageOffers())">{{ t('accountManagement.ui.recheckStorage') }}</Button>
      </section>
      <section v-if="state?.state === 'local' && state.discovery_status === 'unknown' && !accountState.discoveryIssue"
        class="rounded-xl border border-border bg-card p-3 text-sm leading-relaxed text-muted-foreground sm:p-4">
        <p role="alert">{{ t('accountManagement.ui.mnemonicRootRequired') }}</p>
      </section>
      <section v-if="summary || accountState.canUpgrade" class="space-y-3 rounded-xl border border-border bg-card p-3 sm:p-4">
        <h2 ref="recoveryHeading" tabindex="-1" class="text-base font-semibold">
          {{ t(summary && setupMode === 'temporary' ? 'accountManagement.ui.backupSetupTitle' : 'accountManagement.ui.upgradeStorage') }}
        </h2>
        <Button v-if="!summary" variant="outline" :disabled="busy" @click="preflight('paid')">{{ t('accountManagement.ui.upgradeStorage') }}</Button>
        <div v-if="summary" class="space-y-2 rounded-lg bg-muted/60 p-3 text-sm">
          <h3 class="font-medium">{{ t('accountManagement.ui.backupWallets') }}</h3>
          <ul class="divide-y divide-border">
            <li v-for="wallet in summary.wallets" :key="wallet.name" class="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5 py-1.5">
              <span class="min-w-0 break-words">{{ wallet.name }}</span>
              <span class="text-xs text-muted-foreground">{{ t('accountManagement.ui.subaccountCount', {value: wallet.account_count}) }}</span>
            </li>
          </ul>
        </div>
        <template v-if="summary">
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
            <Button v-if="setupMode === 'paid'" :disabled="busy || !option.available || storage?.status === 'ready'" @click="selectStorage(option)">{{ t('common.confirm') }}</Button>
            <Button v-else :disabled="busy || !option.available" @click="activate(option)">{{ t('accountManagement.ui.enableBackup') }}</Button>
          </div>
          <p v-if="storage">{{ storage.status }} <span class="break-all">{{ storage.tx_id }}</span></p>
          <Button v-if="setupMode === 'paid' && storage?.status === 'ready'" :disabled="busy" @click="activate()">{{ t('accountManagement.ui.upgradeStorage') }}</Button>
          <Button variant="outline" :disabled="busy" @click="cancelStorage">{{ t('common.cancel') }}</Button>
        </template>
      </section>
      <p v-if="error && !startSetup" class="break-all text-sm text-destructive" role="alert">{{ error }}</p>
      <Dialog :open="!!payment" @update:open="value => { if (!value) cancelPayment() }">
        <DialogContent class="max-h-[calc(100dvh_-_2rem)] w-[calc(100%_-_2rem)] max-w-md overflow-y-auto rounded-xl bg-background">
          <DialogHeader><DialogTitle>{{ t('accountManagement.dkvsPay') }}</DialogTitle><DialogDescription>{{ t('accountManagement.ui.paidWarning') }}</DialogDescription></DialogHeader>
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
  </LayoutSecond>
</template>
<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useClipboard, useDocumentVisibility } from '@vueuse/core'
import { ArrowRight, Cloud, CloudUpload, Shield, ShieldCheck } from 'lucide-vue-next'
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import AccountReadinessBanner from '@/components/account/AccountReadinessBanner.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { useWalletStore } from '@/store/wallet'
import { useAccountManagementStore } from '@/store/accountManagement'
import { withWalletPassword } from '@/lib/walletPasswordPrompt'
import accountSDK, { accountDisplayMessage } from '@/utils/accountManagement'
import type { PaymentQuote, StorageOption, StorageView, RecoverySummary } from '@/utils/walletViews'
const { t } = useI18n()
const { copy, copied } = useClipboard()
const router = useRouter()
const wallet = useWalletStore(); const accountState = useAccountManagementStore()
const state = computed(() => accountState.status)
const localOnly = computed(() => state.value?.state === 'local' && !accountState.readinessError &&
  state.value.discovery_status === 'inactive')
const storageNotice = computed(() => {
  if (accountState.storageReadFailed) return {
    title: 'accountManagement.ui.storageCheckFailed',
    body: 'accountManagement.ui.configReadFailed',
  }
  if (accountState.storageOffers) return {
    title: 'accountManagement.ui.temporaryUnavailableTitle',
    body: 'accountManagement.ui.temporaryUnavailable',
  }
  return { title: 'accountManagement.ui.checkingStorage', body: '' }
})
const waitingForBackup = computed(() => state.value?.backup_risk === 'notConfigured' &&
  state.value.discovery_status === 'active-waiting-backup')
const autopay = computed(() => state.value?.autopay)
const visibility = useDocumentVisibility()
const pendingFundingTx = ref('')
const visibleFundingTx = computed(() => accountState.sessionReady && visibility.value === 'visible'
  ? pendingFundingTx.value : '')
const busy = ref(false); const error = ref(''); let disposed = false
const summary = ref<RecoverySummary | null>(null); const storage = ref<StorageView | null>(null)
const options = ref<StorageOption[]>([]); const recordCount = ref(100)
const setupMode = ref<'temporary' | 'paid'>('temporary')
const startSetup = computed(() => accountState.canActivate && !summary.value)
const recoveryHeading = ref<HTMLElement | null>(null)
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
  await nextTick()
  recoveryHeading.value?.focus()
})
const loadOptions = () => run(readOptions)
const requestPayment = (quote: PaymentQuote | null, action: () => Promise<void>) => {
  if (quote) { payment.value = quote; paymentAction = action } else void run(action)
}
const cancelPayment = () => { payment.value = null; paymentAction = undefined }
const confirmPayment = () => run(async () => { const action = paymentAction; cancelPayment(); if (action) await action(); await accountState.refresh() })
const confirmStorage = async (option: StorageOption) => {
  try {
    storage.value = await accountSDK.confirmStorage(option)
  } catch (cause) {
    if ((cause as {code?: string})?.code === 'STORAGE_TTL_CHANGED') {
      await readOptions()
      throw new Error(t('accountManagement.ui.retentionChanged'))
    }
    throw cause
  }
}
const selectStorage = (option: StorageOption) => requestPayment(option.payment, async () => {
  await confirmStorage(option)
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

const activate = (option?: StorageOption) => withPassword(async password => {
  try {
    if (option) await confirmStorage(option)
    if (storage.value?.status !== 'ready') return
    await accountSDK.activateRootAccount(password, walletIDs())
    summary.value = null
    storage.value = null
  } finally {
    await accountState.refresh()
  }
})
onMounted(() => { void run(() => accountState.refresh()) })
watch(() => accountState.storagePhase, () => { void accountState.refreshStorageOffers() }, {immediate: true})
onBeforeUnmount(() => { disposed = true; cancelPayment() })
</script>
