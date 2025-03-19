<template>
  <div class="min-h-screen flex items-center justify-center p-4">
    <div class="max-w-md w-full space-y-8">
      <div class="text-center">
        <div class="flex items-center justify-center gap-2 mb-4">
          <h1 class="text-2xl font-bold text-gray-900 dark:text-white">
            Unlock Wallet
          </h1>
        </div>
        <p class="text-gray-600 dark:text-gray-400 mb-8">
          Please enter your wallet password to continue
        </p>
      </div>
      <div>
        <form @submit="onSubmit" class="space-y-6 mb-2">
          <FormField v-slot="{ componentField }" name="password">
            <FormItem>
              <FormLabel>Password</FormLabel>
              <FormControl>
                <Input
                  type="password"
                  placeholder="Enter password..."
                  v-bind="componentField"
                >
                </Input>
              </FormControl>
              <FormMessage />
            </FormItem>
          </FormField>

          <div class="grid grid-cols-1 gap-2">
            <Button type="submit" :disabled="loading">
              <Icon
                v-if="!loading"
                :inline="true"
                class="mr-2 h-4 w-4"
                icon="mdi:lock-open"
              />
              <Icon
                v-else
                :inline="true"
                class="mr-2 h-4 w-4 animate-spin"
                icon="mdi:loading"
              />
              Unlock
            </Button>
          </div>
        </form>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useToast } from '@/components/ui/toast'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useForm } from 'vee-validate'
import { Icon } from '@iconify/vue'
import { toTypedSchema } from '@vee-validate/zod'
import * as z from 'zod'
import {
  FormField,
  FormItem,
  FormLabel,
  FormControl,
  FormMessage,
} from '@/components/ui/form'

import { useChannelStore, useWalletStore } from '@/store'
import walletManager from '@/utils/sat20'
import { unlockPasswordSchema } from '@/utils/validation'
import satsnetStp from '@/utils/stp'
import { hashPassword } from '@/utils/crypto'

const formSchema = toTypedSchema(unlockPasswordSchema)

const form = useForm({
  validationSchema: formSchema,
})

const walletStore = useWalletStore()
const channelStore = useChannelStore()
const router = useRouter()
const route = useRoute()
const { toast } = useToast()
const loading = ref(false)

const showToast = (
  variant: 'default' | 'destructive',
  title: string,
  description: string | Error
) => {
  toast({
    variant,
    title,
    description:
      typeof description === 'string' ? description : description.message,
  })
}

const onSubmit = form.handleSubmit(async (values) => {
  loading.value = true

  // Hash the password using the imported function
  const hashedPassword = await hashPassword(values.password)

  const [err, result] = await walletStore.unlockWallet(hashedPassword)

  if (!err && result) {
    const redirectPath = route.query.redirect as string
    router.push(redirectPath || '/wallet')
  } else if (err) {
    showToast(
      'destructive',
      'Error',
      err instanceof Error ? err.message : JSON.stringify(err)
    )
    loading.value = false
  } else {
    showToast('destructive', 'Error', 'Failed to unlock wallet')
    loading.value = false
  }
})

// const deleteWallet = async () => {
//   // localStorage.clear()
//   // location.href = '/'
// }
</script>
