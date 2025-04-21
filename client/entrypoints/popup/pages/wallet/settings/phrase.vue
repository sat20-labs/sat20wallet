<template>
  <LayoutSecond title="Recovery Phrase">
    <div
      v-if="!mnemonicWords.length"
      class="flex flex-col items-center justify-center pt-8"
    >
      <div class="text-center mb-6">
        <h3 class="text-lg font-semibold text-red-600 mb-2">⚠️ Warning</h3>
        <ul
          class="text-sm text-left space-y-2 bg-zinc-800 dark:bg-red-900/20 p-4 rounded-lg"
        >
          <li>• Never share your recovery phrase with anyone</li>
          <li>• Never input these words on any website</li>
          <li>• Store them in a secure location</li>
          <li>• Loss of recovery phrase means loss of funds</li>
        </ul>
      </div>
      <div class="space-y-4 w-full">
        <Input
          type="password"
          class="w-full"
          v-model="password"
          placeholder="Enter password to verify"
          @keyup.enter="verifyPassword"
        />
        <Button
          :disabled="loading"
          class="w-full"
          @click="verifyPassword"
          variant="default"
        >
          {{ loading ? 'Verifying...' : 'Verify' }}
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
      <Button variant="default" @click="copyMnemonic" class="mt-4">
        Copy Mnemonic
      </Button>
    </div>
  </LayoutSecond>
</template>

<script setup lang="ts">
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import walletManager from '@/utils/sat20'
import { useWalletStore } from '@/store'
import { useClipboard } from '@vueuse/core'
import { hashPassword } from '@/utils/crypto'
const walletStore = useWalletStore()
const { walletId } = storeToRefs(walletStore)
const password = ref<string | number>('')
const loading = ref(false)
const isVerified = ref(false)
const mnemonicPhrase = ref('')

// Add computed property for mnemonic words
const mnemonicWords = computed(() =>
  mnemonicPhrase.value.split(' ').filter((word) => word.length > 0)
)
const { copy, copied, isSupported } = useClipboard()

const copyHandler = () => {}
const verifyPassword = async () => {
  if (!password.value) {
    // toast.add({
    //   title: 'Error',
    //   description: 'Please enter password',
    //   color: 'red',
    // })
    return
  }
  console.log('verify password')
  console.log(password.value)

  loading.value = true
  const hashedPassword = await hashPassword(password.value as string)
  const [err, result] = await walletManager.getMnemonice(
    walletId.value,
    hashedPassword
  )
  loading.value = false
  console.log('verify password result')
  console.log(result)

  if (err || !result?.mnemonic) {
    // toast.add({
    //   title: 'Error',
    //   description: 'Verification failed',
    //   color: 'red',
    // })
    return
  }
  mnemonicPhrase.value = result.mnemonic
}

const copyMnemonic = () => {
  if (isSupported && mnemonicPhrase.value) {
    copy(mnemonicPhrase.value)
  }
}
</script>
