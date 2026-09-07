<template>
  <Dialog :open="open" @update:open="value => { if (!value) finish() }">
    <DialogContent class="sm:max-w-[425px]">
      <DialogHeader>
        <DialogTitle>{{ $t('walletPasswordPrompt.title') }}</DialogTitle>
        <DialogDescription>{{ $t('walletPasswordPrompt.description') }}</DialogDescription>
      </DialogHeader>
      <form class="space-y-4" @submit.prevent="confirm">
        <input ref="passwordInput" type="password" autocomplete="current-password" required
          :disabled="busy" :aria-label="$t('unlock.passwordPlaceholder')"
          :placeholder="$t('unlock.passwordPlaceholder')"
          class="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" />
        <p v-if="error" role="alert" class="text-sm text-destructive">{{ error }}</p>
        <DialogFooter>
          <Button type="button" variant="secondary" @click="finish()">{{ $t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy">{{ $t('common.confirm') }}</Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>

<script setup lang="ts">
import { ref, watch, onBeforeUnmount } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { registerWalletPasswordPrompt } from '@/lib/walletPasswordPrompt'
import { subscribeWalletIdentity } from '@/lib/identity-boundary'
import { CredentialAttemptLimiter } from '@/lib/credential-rate-limit'
import walletManager from '@/utils/sat20'

const { t } = useI18n()
const route = useRoute()
const open = ref(false)
const busy = ref(false)
const error = ref('')
const passwordInput = ref<HTMLInputElement | null>(null)
const limiter = new CredentialAttemptLimiter()
let resolve: ((password: string | undefined) => void) | undefined

const finish = (password?: string) => {
  if (passwordInput.value) passwordInput.value.value = ''
  const done = resolve
  resolve = undefined
  open.value = false
  error.value = ''
  done?.(password)
}

const unregister = registerWalletPasswordPrompt(() => {
  if (resolve || busy.value) return Promise.resolve(undefined)
  error.value = ''
  open.value = true
  return new Promise<string | undefined>(done => { resolve = done })
})

const confirm = async () => {
  if (busy.value || !resolve || !passwordInput.value?.value) return
  const currentRequest = resolve
  let password = passwordInput.value.value
  passwordInput.value.value = ''
  busy.value = true
  error.value = ''
  try {
    limiter.assertAllowed()
    // Warm unlock only verifies the password; it does not change the UI session
    // or restart/switch the SDK runtime.
    const [failure] = await walletManager.unlockWallet(password)
    if (resolve !== currentRequest) return
    if (failure) {
      limiter.recordFailure()
      error.value = t('unlock.invalidPassword')
      return
    }
    limiter.reset()
    finish(password)
  } catch {
    if (resolve === currentRequest) error.value = t('unlock.unlockFailed')
  } finally {
    password = ''
    busy.value = false
  }
}

const unsubscribe = subscribeWalletIdentity(identity => {
  if (identity.phase !== 'READY') finish()
})
watch(() => route.fullPath, () => finish())
onBeforeUnmount(() => { finish(); unregister(); unsubscribe() })
</script>
