<template>
  <LayoutScroll>
    <div class="p-4 space-y-5 max-w-xl mx-auto">
      <div class="text-center space-y-1">
        <h1 class="text-2xl font-semibold">{{ rehearsalOnly ? t('accountManagement.ui.rehearsalTitle') : t('accountManagement.ui.restoreTitle') }}</h1>
        <p v-if="rehearsalOnly" class="text-sm text-muted-foreground">{{ t('accountManagement.ui.rehearsalIntro') }}</p>
        <template v-else>
          <p class="text-sm text-muted-foreground">{{ t('accountManagement.ui.restoreIntro') }}</p>
          <p class="text-sm text-muted-foreground">{{ t('accountManagement.ui.resumeHint') }}</p>
        </template>
      </div>

      <section v-if="step === 1" class="space-y-3">
        <label class="font-medium">{{ t('accountManagement.ui.recoveryFile') }}</label>
        <Textarea v-model="recoveryFile" rows="6" spellcheck="false" autocomplete="off" autocapitalize="off"
          :placeholder="t('accountManagement.ui.pasteRecoveryFile')" />
        <Button class="w-full" :disabled="busy || !recoveryFile" @click="loadRecovery">
          <Icon v-if="busy" icon="lucide:loader-2" class="mr-2 h-4 w-4 animate-spin" />
          {{ t('accountManagement.ui.loadBackup') }}
        </Button>
      </section>

      <section v-else-if="step === 2 && loaded" class="space-y-4">
        <h2 class="font-medium">{{ t('accountManagement.ui.provideFactors') }}</h2>
        <div v-for="(question,index) in loaded.questions" :key="question.id" class="space-y-1">
          <label class="text-sm">{{ question.prompt }}</label><Input v-model="answers[index]" type="password" autocomplete="off" />
        </div>
        <Button class="w-full" :disabled="busy" @click="previewRecovery">{{ t('accountManagement.ui.previewRecovery') }}</Button>
      </section>

      <section v-else-if="step === 4 && preview" class="space-y-4">
        <h2 class="font-medium">{{ rehearsalOnly ? t('accountManagement.ui.factorsVerified') : t('accountManagement.ui.confirmRecovery') }}</h2>
        <div v-for="wallet in preview.wallets" :key="wallet.name" class="rounded-lg border p-3">
          <div class="font-medium">{{ wallet.name }}</div>
          <div class="text-sm text-muted-foreground">{{ t('accountManagement.ui.subaccountCount', { value: wallet.account_count }) }}</div>
          <div class="text-xs mt-1">{{ t('accountManagement.ui.didList', { value: wallet.dids.join(t('accountManagement.ui.listSeparator')) }) }}</div>
        </div>
        <template v-if="rehearsalOnly">
          <p class="text-sm text-muted-foreground">{{ t('accountManagement.ui.rehearsalVerified') }}</p>
          <Button class="w-full" :disabled="busy" @click="cancelRecovery">{{ t('accountManagement.ui.finishRehearsal') }}</Button>
        </template>
        <template v-else>
          <p id="restore-password-help" class="text-sm text-muted-foreground">
            {{ t('accountManagement.ui.passwordHelp') }}
          </p>
          <div class="space-y-2">
            <label for="restore-local-password" class="block text-sm font-medium">{{ t('accountManagement.ui.newDevicePassword') }}</label>
            <Input id="restore-local-password" v-model="password" type="password" :placeholder="t('accountManagement.ui.setPassword')"
              autocomplete="new-password" aria-describedby="restore-password-help" />
          </div>
          <div class="space-y-2">
            <label for="restore-confirm-password" class="block text-sm font-medium">{{ t('accountManagement.ui.repeatUnlockPassword') }}</label>
            <Input id="restore-confirm-password" v-model="confirmPassword" type="password" :placeholder="t('accountManagement.ui.repeatPassword')"
              autocomplete="new-password" aria-describedby="restore-password-help" />
          </div>
          <Button class="w-full" :disabled="busy" @click="commitRecovery">{{ t('accountManagement.ui.restoreAll') }}</Button>
        </template>
      </section>

      <section v-else-if="step === 5" class="text-center space-y-3 py-10">
        <Icon icon="lucide:badge-check" class="h-14 w-14 text-green-500 mx-auto" />
        <h2 class="text-xl font-semibold">{{ t('accountManagement.ui.restoreSuccess') }}</h2>
        <p class="text-sm text-muted-foreground">{{ t('accountManagement.ui.reloading') }}</p>
      </section>

      <section v-else-if="step === 7" class="space-y-3" data-testid="partial-recovery">
        <h2 class="font-medium">{{ t('accountManagement.ui.restorePartial') }}</h2>
        <p class="text-sm text-muted-foreground">{{ t('accountManagement.ui.restorePartialHint') }}</p>
        <ul><li v-for="module in failedModules" :key="module.name">{{ module.name }}</li></ul>
        <Button class="w-full" :disabled="busy" @click="reloadWallet">{{ t('accountManagement.ui.reloadWallet') }}</Button>
      </section>

      <section v-else-if="step === 6" class="space-y-3">
        <h2 class="font-medium">{{ t('accountManagement.ui.uiReconcileFailed') }}</h2>
        <p class="text-sm text-muted-foreground">{{ t('accountManagement.ui.savedReloadHint') }}</p>
        <Button class="w-full" :disabled="busy" @click="reloadWallet">{{ t('accountManagement.ui.reloadWallet') }}</Button>
      </section>

      <Alert v-if="error" variant="destructive"><AlertDescription>{{ error }}</AlertDescription></Alert>
      <Button v-if="step < 5" variant="ghost" class="w-full" :disabled="busy && step === 4" @click="cancelRecovery">{{ t('accountManagement.ui.cancel') }}</Button>
    </div>
  </LayoutScroll>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Icon } from '@iconify/vue'
import LayoutScroll from '@/components/layout/LayoutScroll.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Alert, AlertDescription } from '@/components/ui/alert'
import accountSDK, { accountDisplayMessage } from '@/utils/accountManagement'
import type { RecoveryView, RecoverySummary, ModuleView } from '@/utils/walletViews'

const { t } = useI18n()
const router = useRouter()
const route = useRoute()
const rehearsalOnly = route.query.mode === 'rehearsal'
const busy = ref(false)
const error = ref('')
const failedModules = ref<ModuleView[]>([])
const step = ref(1)
const recoveryFile = ref('')
const loaded = ref<RecoveryView | null>(null)
const answers = ref<string[]>([])
const preview = ref<RecoverySummary | null>(null)
const password = ref('')
const confirmPassword = ref('')

const sessionId = computed(() => loaded.value?.session_id || '')
let disposed = false

const clearRecovery = () => {
  const id = sessionId.value
  step.value = 1
  loaded.value = null
  preview.value = null
  answers.value = []
  recoveryFile.value = password.value = confirmPassword.value = ''
  if (id) return accountSDK.abortRecovery(id).catch(() => undefined)
}
const cancelRecovery = async () => {
  disposed = true
  await clearRecovery()
  const returnPath = rehearsalOnly ? '/wallet/setting/account-management' : '/'
  if (window.history.state?.back === returnPath) router.back()
  else await router.replace(returnPath)
}
onBeforeUnmount(() => { disposed = true; void clearRecovery() })
onMounted(async () => {
  if (!rehearsalOnly) return
  try {
    const status = await accountSDK.getAccountView()
    if (disposed) return
    if (!status.account_id || !status.locator) error.value = t('accountManagement.ui.recoveryNotConfigured')
  } catch (e: any) { if (!disposed) error.value = accountDisplayMessage(t, e?.message) || t('accountManagement.ui.readRecoveryFailed') }
})

const run = async (task: () => Promise<void>) => {
  busy.value = true
  error.value = ''
  try { await task() } catch (e: any) {
    if (e?.message === 'recovery session expired') {
      await clearRecovery()
      error.value = t('accountManagement.ui.sessionExpired')
    } else {
      error.value = accountDisplayMessage(t, e?.message) || t('accountManagement.ui.recoveryFailed')
    }
  } finally { busy.value = false }
}

const loadRecovery = () => run(async () => {
  const result = await accountSDK.loadRecovery(recoveryFile.value, rehearsalOnly)
  recoveryFile.value = ''
  if (disposed) {
    await accountSDK.abortRecovery(result.session_id)
    return
  }
  loaded.value = result
  answers.value = loaded.value.questions.map(() => '')
  step.value = 2
})

const previewRecovery = () => run(async () => {
  if (!loaded.value) return
  const value = await accountSDK.previewRecovery(sessionId.value, {
    answers: loaded.value.questions.map((question,index) => ({question_id: question.id, text: answers.value[index] || ''})).filter(item => item.text),
    guardian_response: '',
  })
  if (!value.summary) throw new Error(t('accountManagement.ui.recoveryFailed'))
  loaded.value = value; preview.value = value.summary; step.value = 4
})

const commitRecovery = () => run(async () => {
  if (!loaded.value?.can_commit) throw new Error(t('accountManagement.ui.readOnlyCannotCommit'))
  if (password.value.length < 6 || password.value !== confirmPassword.value) throw new Error(t('accountManagement.ui.passwordMismatch'))
  const result = await accountSDK.commitRecovery(sessionId.value, password.value)
  // The SDK has consumed the session and committed the authoritative database.
  // Every subsequent failure is UI reconciliation; never offer commit again.
  step.value = 6
  loaded.value = null
  answers.value = []
  password.value = confirmPassword.value = ''
  if (!result.complete) {
    if (!result.failed_modules.length) {
      reloadWallet()
      return
    }
    failedModules.value = result.failed_modules
    step.value = 7
    return
  }
  // The next unlock reads catalog and persisted selection from the SDK.
  step.value = 5
  setTimeout(reloadWallet, 500)
})

const reloadWallet = () => {
  window.location.hash = '#/unlock'
  window.location.reload()
}

</script>
