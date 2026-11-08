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
        <label class="font-medium">{{ t('accountManagement.ui.recoveryCode') }}</label>
        <Textarea v-model="locator" rows="6" :placeholder="t('accountManagement.ui.pasteLocator')" />
        <Button class="w-full" :disabled="busy || !locator" @click="loadRecovery">
          <Icon v-if="busy" icon="lucide:loader-2" class="mr-2 h-4 w-4 animate-spin" />
          {{ t('accountManagement.ui.loadBackup') }}
        </Button>
      </section>

      <section v-else-if="step === 2" class="space-y-4">
        <h2 class="font-medium">{{ t('accountManagement.ui.answerQuestions') }}</h2>
        <div v-for="(question, index) in loaded.questions" :key="question.id" class="space-y-1">
          <label class="text-sm">{{ question.prompt }}</label>
          <Input v-model="answers[index]" type="password" autocomplete="off" />
        </div>
        <Button class="w-full" :disabled="busy" @click="recoverKnowledge">
          {{ t('accountManagement.ui.recoverShare') }}
        </Button>
        <Button v-if="loaded.locator.recovery_mode === '2of3' && loaded.has_guardian_location"
          variant="outline" class="w-full" :disabled="busy" @click="step = 3">
          {{ t('accountManagement.ui.shareAndGuardian') }}
        </Button>
      </section>

      <section v-else-if="step === 3" class="space-y-4">
        <h2 class="font-medium">{{ t('accountManagement.ui.provideFactors') }}</h2>
        <p class="text-sm text-muted-foreground">{{ knowledgeReady ? t('accountManagement.ui.chooseCompanion') : t('accountManagement.ui.requireCompanions') }}</p>
        <Textarea v-model="userShare" rows="5" :placeholder="t('accountManagement.ui.pasteOptionalShare')" />
        <Button variant="outline" class="w-full" :disabled="busy || !userShare" @click="acceptUserShare">{{ t('accountManagement.ui.useShare') }}</Button>

        <div v-if="loaded.guardian && loaded.has_guardian_location" class="rounded-lg border p-3 space-y-2">
          <Button variant="outline" class="w-full" :disabled="busy" @click="createGuardianRequest">{{ t('accountManagement.ui.createGuardianRequest') }}</Button>
          <Textarea v-if="guardianRequest" :model-value="guardianRequest" readonly rows="6" />
          <Button v-if="guardianRequest" variant="ghost" class="w-full" @click="copyText(guardianRequest)">{{ t('accountManagement.ui.copyRequest') }}</Button>
          <Textarea v-model="guardianResponse" rows="6" :placeholder="t('accountManagement.ui.pasteGuardianResponse')" />
          <Button variant="outline" class="w-full" :disabled="busy || !guardianResponse" @click="acceptGuardianResponse">{{ t('accountManagement.ui.useGuardianResponse') }}</Button>
        </div>

        <Button class="w-full" :disabled="busy || !companionReady" @click="previewRecovery">{{ t('accountManagement.ui.previewRecovery') }}</Button>
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
import accountSDK, { accountDisplayMessage, type AccountRecoverySummary } from '@/utils/accountManagement'

const { t } = useI18n()
const router = useRouter()
const route = useRoute()
const rehearsalOnly = route.query.mode === 'rehearsal'
const expectedAccountId = ref('')
const busy = ref(false)
const error = ref('')
const step = ref(1)
const locator = ref('')
const loaded = ref<any>(null)
const answers = ref<string[]>([])
const userShare = ref('')
const guardianRequest = ref('')
const guardianResponse = ref('')
const knowledgeReady = ref(false)
const userShareReady = ref(false)
const guardianReady = ref(false)
const companionReady = computed(() => Number(knowledgeReady.value) + Number(userShareReady.value) + Number(guardianReady.value) >= 2)
const preview = ref<AccountRecoverySummary | null>(null)
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
  userShare.value = guardianRequest.value = guardianResponse.value = password.value = confirmPassword.value = ''
  knowledgeReady.value = userShareReady.value = guardianReady.value = false
  if (id) return accountSDK.abortSession(id).catch(() => undefined)
}
const cancelRecovery = async () => {
  disposed = true
  await clearRecovery()
  await router.push(rehearsalOnly ? '/wallet/setting/account-management' : '/')
}
onBeforeUnmount(() => { disposed = true; void clearRecovery() })
onMounted(async () => {
  if (!rehearsalOnly) return
  try {
    const status = await accountSDK.status()
    if (disposed) return
    expectedAccountId.value = status.account_id || ''
    locator.value = status.public_locator || ''
    if (!expectedAccountId.value || !locator.value) error.value = t('accountManagement.ui.recoveryNotConfigured')
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
  const result = await accountSDK.loadRecovery(locator.value.trim())
  if (disposed) {
    await accountSDK.abortSession(result.session_id)
    return
  }
  loaded.value = result
  answers.value = loaded.value.questions.map(() => '')
  step.value = 2
})

const recoverKnowledge = () => run(async () => {
  const attempts = answers.value.map((answer, index) => ({ question_id: loaded.value.questions[index].id, answer })).filter(item => item.answer)
  if (attempts.length < 2) throw new Error(t('accountManagement.ui.answerAtLeastTwo'))
  await accountSDK.recoverKnowledge(sessionId.value, attempts)
  knowledgeReady.value = true
  step.value = 3
})

const acceptUserShare = () => run(async () => {
  await accountSDK.setUserShare(sessionId.value, userShare.value.trim())
  userShareReady.value = true
})

const createGuardianRequest = () => run(async () => {
  guardianRequest.value = (await accountSDK.createGuardianRequest(sessionId.value)).request
})

const acceptGuardianResponse = () => run(async () => {
  await accountSDK.consumeGuardianResponse(sessionId.value, guardianResponse.value.trim())
  guardianReady.value = true
})

const previewRecovery = () => run(async () => {
  const summary = (await accountSDK.previewRecovery(sessionId.value)).summary
  if (rehearsalOnly && (!expectedAccountId.value || summary.account_id !== expectedAccountId.value)) {
    throw new Error(t('accountManagement.ui.wrongAccount'))
  }
  preview.value = summary
  step.value = 4
})

const commitRecovery = () => run(async () => {
  if (rehearsalOnly) throw new Error(t('accountManagement.ui.readOnlyCannotCommit'))
  if (password.value.length < 6 || password.value !== confirmPassword.value) throw new Error(t('accountManagement.ui.passwordMismatch'))
	await accountSDK.commitRecovery(sessionId.value, password.value)
  // The SDK has consumed the session and committed the authoritative database.
  // Every subsequent failure is UI reconciliation; never offer commit again.
  step.value = 6
  loaded.value = null
  answers.value = []
  userShare.value = guardianResponse.value = password.value = confirmPassword.value = ''
  // The next unlock reads catalog and persisted selection from the SDK.
  step.value = 5
  setTimeout(reloadWallet, 500)
})

const reloadWallet = () => {
  window.location.hash = '#/unlock'
  window.location.reload()
}

const copyText = async (value: string) => {
  if (value) await navigator.clipboard.writeText(value)
}
</script>
