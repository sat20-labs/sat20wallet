<template>
  <div class="min-h-screen flex items-center justify-center p-4">
    <div class="max-w-md w-full space-y-8">
      <div class="text-center">
        <div class="flex items-center justify-center gap-2 mb-4">
          <LockClosedIcon class="h-8 w-8 text-primary" />
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
                  <template #prefix>
                    <KeyIcon class="h-4 w-4 text-gray-400" />
                  </template>
                </Input>
              </FormControl>
              <FormMessage />
            </FormItem>
          </FormField>

          <div class="grid grid-cols-1 gap-2">
            <Button type="submit" :disabled="loading">
              <LockOpenIcon v-if="!loading" class="mr-2 h-4 w-4" />
              <Loader2Icon v-else class="mr-2 h-4 w-4 animate-spin" />
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
import { toTypedSchema } from '@vee-validate/zod'
import * as z from 'zod'
import {
  FormField,
  FormItem,
  FormLabel,
  FormControl,
  FormMessage,
} from '@/components/ui/form'
// import { LockClosedIcon, KeyIcon, LockOpenIcon, Loader2Icon } from 'lucide-vue-next'
import { useGlobalStore, useWalletStore } from '@/store'
import walletManager from '@/utils/sat20'

const formSchema = toTypedSchema(
  z.object({
    password: z.string().min(1, 'Password is required'),
  })
)

const form = useForm({
  validationSchema: formSchema,
})

const walletStore = useWalletStore()
const { accountIndex } = storeToRefs(walletStore)
const router = useRouter()
const route = useRoute()
const { toast } = useToast()
const loading = ref(false)

const showToast = (variant: 'default' | 'destructive', title: string, description: string | Error) => {
  toast({
    variant,
    title,
    description: typeof description === 'string' ? description : description.message
  })
}

const onSubmit = form.handleSubmit(async (values) => {
  loading.value = true
  const [err, result] = await walletManager.unlockWallet(values.password)

  if (!err && result) {
    const { walletId } = result
    walletStore.setWalletId(walletId)
    await walletStore.getWalletInfo()
    const redirectPath = route.query.redirect as string
    router.push(redirectPath || '/wallet')
  } else if (err) {
    showToast('destructive', 'Error', err instanceof Error ? err.message : err)
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
