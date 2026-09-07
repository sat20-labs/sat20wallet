<template>
  <LayoutSecond :title="$t('phrase.title')">
    <div
      v-if="!mnemonicWords.length"
      class="flex flex-col items-center justify-center pt-8"
    >
      <div class="text-center mb-6">
        <h3 class="text-lg font-semibold text-red-600 mb-2">⚠️ {{ $t('phrase.warningTitle') }}</h3>
        <ul
          class="text-sm text-left space-y-2 bg-zinc-800 dark:bg-red-900/20 p-4 rounded-lg"
        >
          <li>• {{ $t('phrase.warning1') }}</li>
          <li>• {{ $t('phrase.warning2') }}</li>
          <li>• {{ $t('phrase.warning3') }}</li>
          <li>• {{ $t('phrase.warning4') }}</li>
        </ul>
      </div>
      <div class="space-y-4 w-full">
        <Input
          type="password"
          class="w-full"
          v-model="password"
          :placeholder="$t('phrase.passwordPlaceholder')"
          @keyup.enter="verifyPassword"
        />
        <Button
          :disabled="loading"
          class="w-full"
          @click="verifyPassword"
          variant="default"
        >
          {{ loading ? $t('phrase.verifying') : $t('phrase.verify') }}
        </Button>
      </div>
    </div>

    <div v-else class="text-center">
      <div
        class="grid grid-cols-3 gap-3 my-5 p-4 rounded-lg"
      >
        <div
          v-for="(word, index) in mnemonicWords"
          :key="index"
          class="flex dark:bg-gray-600 items-center p-2 rounded shadow-sm"
        >
          <span
            class="select-none text-gray-600 dark:text-gray-300 text-sm mr-2"
            >{{ index + 1 }}.</span
          >
          <span class="font-medium">{{ word }}</span>
        </div>
      </div>
	  <p class="mt-4 text-xs text-muted-foreground">Clipboard export is disabled. Record the words in a secure offline location.</p>
    </div>
  </LayoutSecond>
</template>

<script setup lang="ts">
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import walletManager from '@/utils/sat20'
import { ref, computed, onBeforeUnmount } from 'vue'
import { storeToRefs } from 'pinia'
import { useWalletStore } from '@/store/wallet'
import { useToast } from '@/components/ui/toast-new'
import { onBeforeRouteLeave } from 'vue-router'
import { beginMnemonicView } from '@/lib/sensitive-session'
import { CredentialAttemptLimiter } from '@/lib/credential-rate-limit'
import { useApproveStore } from '@/store'
const walletStore = useWalletStore()
const { walletId } = storeToRefs(walletStore)
const password = ref<string | number>('')
const loading = ref(false)
const { toast } = useToast()
const isVerified = ref(false)
const mnemonicPhrase = ref('')
const approveStore = useApproveStore()
const limiter = new CredentialAttemptLimiter()
let endMnemonicView: (() => void) | null = null

// Add computed property for mnemonic words
const mnemonicWords = computed(() =>
  mnemonicPhrase.value.split(' ').filter((word) => word.length > 0)
)
const verifyPassword = async () => {
  if (!password.value) {
    toast({
      title: 'Error',
      description: 'Please enter password',
      variant: 'destructive'
    })
    return
  }
	try {
	  limiter.assertAllowed()
	} catch (error) {
	  toast({ title: 'Error', description: (error as Error).message, variant: 'destructive' })
	  return
	}
  loading.value = true
	const [err, result] = await walletManager.getMnemonice(
		walletId.value as any,
		password.value as string
  )
  loading.value = false

  if (err || !result?.mnemonic) {
	limiter.recordFailure()
    toast({
      title: 'Error',
      description: 'Verification failed',
      variant: 'destructive'
    })
    return
  }
	limiter.reset()
	if (approveStore.isVisible.value) approveStore.hideApprove()
	endMnemonicView ??= beginMnemonicView()
  mnemonicPhrase.value = result.mnemonic
}

const clearSensitiveState = () => {
	mnemonicPhrase.value = ''
	password.value = ''
	isVerified.value = false
	endMnemonicView?.()
	endMnemonicView = null
}

onBeforeRouteLeave(() => clearSensitiveState())
onBeforeUnmount(clearSensitiveState)
</script>
