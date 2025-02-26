<template>
  <div class="pt-20 p-4">
    <div class="mb-4">
      <h1 class="text-2xl font-bold">
        {{ step === 1 ? 'Create New Wallet' : 'Save your recovery phrase' }}
      </h1>
      <p class="text-muted-foreground">
        {{
          step === 1
            ? 'Set up your wallet password'
            : 'Save your recovery phrase'
        }}
      </p>
    </div>
    <form @submit.prevent="onSubmit">
      <div v-if="step === 1" class="space-y-4">
        <FormField v-slot="{ componentField }" name="password">
          <FormItem>
            <FormLabel>Password</FormLabel>
            <FormControl>
              <Input
                type="password"
                v-bind="componentField"
                placeholder="Enter password"
              />
            </FormControl>
            <FormDescription>
              Password must be at least 8 characters with uppercase and number
            </FormDescription>
            <FormMessage />
          </FormItem>
        </FormField>

        <FormField v-slot="{ componentField }" name="confirmPassword">
          <FormItem>
            <FormLabel>Confirm Password</FormLabel>
            <FormControl>
              <Input
                type="password"
                v-bind="componentField"
                placeholder="Confirm password"
              />
            </FormControl>
            <FormMessage />
          </FormItem>
        </FormField>
      </div>

      <div v-else class="space-y-4">
        <Alert variant="destructive">
          <AlertTriangle class="h-4 w-4" />
          <AlertDescription>
            Never share your recovery phrase. Store it securely offline.
          </AlertDescription>
        </Alert>
        <div class="relative">
          <div class="grid grid-cols-3 gap-2 p-4 bg-muted rounded-lg">
            <div
              v-for="(word, i) in mnemonic.split(' ')"
              :key="i"
              class="flex items-center space-x-2"
            >
              <span class="text-muted-foreground">{{ i + 1 }}.</span>
              <span :class="showMnemonic ? '' : 'blur-sm select-none'">{{
                word
              }}</span>
            </div>
          </div>
          <Button
            variant="ghost"
            size="icon"
            class="absolute top-2 right-2"
            @click="toggleShowMnemonic"
          >
            <EyeOff v-if="showMnemonic" class="h-4 w-4" />
            <Eye v-else class="h-4 w-4" />
          </Button>
        </div>
        <div class="flex justify-center">
          <Button variant="outline" @click="handleCopyMnemonic" class="w-full">
            <Copy class="mr-2 h-4 w-4" />
            Copy to clipboard
          </Button>
        </div>
      </div>

      <div class="flex justify-between mt-4">
        <Button variant="outline" type="button">
          <RouterLink to="/"> Cancel </RouterLink>
        </Button>
        <Button v-if="step === 1" type="submit">
          <Loader2Icon v-if="loading" class="mr-2 h-4 w-4 animate-spin" />
          {{ loading ? 'Creating...' : 'Continue' }}
        </Button>
        <Button v-else as-child>
          <RouterLink to="/wallet"> I've saved my recovery phrase </RouterLink>
        </Button>
      </div>
    </form>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import * as z from 'zod'
import {
  Form,
  FormField,
  FormItem,
  FormLabel,
  FormControl,
  FormDescription,
  FormMessage,
} from '@/components/ui/form'
import { useRouter } from 'vue-router'
import { useToast } from '@/components/ui/toast'
import { Button } from '@/components/ui/button'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Input } from '@/components/ui/input'
import { useClipboard } from '@vueuse/core'
import { Eye, EyeOff, Copy, AlertTriangle, Loader2Icon } from 'lucide-vue-next'
import walletManager from '@/utils/sat20'
import { useWalletStore } from '@/store'

const { toast } = useToast()
const loading = ref(false)
const step = ref(1)
const walletStore = useWalletStore()
const mnemonic = ref('')
const showMnemonic = ref(false)
const { copy, isSupported } = useClipboard()

const formSchema = toTypedSchema(
  z
    .object({
      password: z
        .string()
        .min(8, 'Password must be at least 8 characters')
        .regex(/[A-Z]/, 'Password must contain at least one uppercase letter')
        .regex(/[0-9]/, 'Password must contain at least one number'),
      confirmPassword: z.string().min(1, 'Please confirm your password'),
    })
    .refine((data) => data.password === data.confirmPassword, {
      message: "Passwords don't match",
      path: ['confirmPassword'],
    })
)

const form = useForm({
  validationSchema: formSchema,
})

const onSubmit = form.handleSubmit(async (values) => {
  if (loading.value) return

  loading.value = true
  const [err, result] = await walletStore.createWallet(values.password)
  loading.value = false

  if (!err && result) {
    mnemonic.value = result as string
    step.value = 2
    return
  }

  toast({
    variant: 'destructive',
    title: 'Error',
    description: err instanceof Error ? err.message : 'Failed to create wallet',
  })
})

const handleCopyMnemonic = async () => {
  if (!isSupported.value) {
    toast({
      variant: 'destructive',
      description: 'Clipboard not supported',
    })
    return
  }

  await copy(mnemonic.value)
  toast({
    description: 'Recovery phrase copied',
  })
}

const toggleShowMnemonic = () => {
  showMnemonic.value = !showMnemonic.value
}
</script>
