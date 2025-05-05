<template>
  <LayoutScroll>
    <div class="p-1">
      <div class="flex flex-col items-center justify-center gap-2 mb-4">
        <img src="@/assets/sat20-logo.svg" alt="ORDX" class="w-12 h-12 mb-2" />
        <h1 class="text-xl font-semibold">{{ $t('import.title') }}</h1>
      </div>
      <p class="text-muted-foreground mb-4 text-center">
        {{ $t('import.subtitle') }}
      </p>

      <form @submit="onSubmit">
        <Tabs v-model="tab" class="space-y-4">
          <TabsList class="grid w-full grid-cols-2">
            <TabsTrigger value="mnemonic">{{ $t('import.recoveryPhraseTab') }}</TabsTrigger>
            <TabsTrigger value="private-key">{{ $t('import.privateKeyTab') }}</TabsTrigger>
          </TabsList>

          <TabsContent value="mnemonic">
            <Alert>
              <AlertDescription>
                {{ $t('import.recoveryPhraseAlert') }}
              </AlertDescription>
            </Alert>

            <FormField v-slot="{ componentField }" name="mnemonic">
              <FormItem>
                <FormLabel>{{ $t('import.recoveryPhraseLabel') }}</FormLabel>
                <FormControl>
                  <Textarea
                    v-bind="componentField"
                    :placeholder="$t('import.recoveryPhrasePlaceholder')"
                    rows="3"
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            </FormField>
          </TabsContent>

          <TabsContent value="private-key">
            <Alert>
              <KeyRound class="h-4 w-4" />
              <AlertDescription>
                {{ $t('import.privateKeyAlert') }}
              </AlertDescription>
            </Alert>

            <FormField v-slot="{ componentField }" name="privateKey">
              <FormItem>
                <FormLabel>{{ $t('import.privateKeyLabel') }}</FormLabel>
                <FormControl>
                  <Input
                    type="password"
                    v-bind="componentField"
                    :placeholder="$t('import.privateKeyPlaceholder')"
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            </FormField>
          </TabsContent>

          <div class="space-y-4 pt-4 border-t">
            <FormField v-slot="{ componentField }" name="password">
              <FormItem>
                <FormLabel>{{ $t('import.newPasswordLabel') }}</FormLabel>
                <FormControl>
                  <Input
                    type="password"
                    v-bind="componentField"
                    :placeholder="$t('import.newPasswordPlaceholder')"
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            </FormField>

            <FormField v-slot="{ componentField }" name="confirmPassword">
              <FormItem>
                <FormLabel>{{ $t('import.confirmPasswordLabel') }}</FormLabel>
                <FormControl>
                  <Input
                    type="password"
                    v-bind="componentField"
                    :placeholder="$t('import.confirmPasswordPlaceholder')"
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            </FormField>
          </div>
        </Tabs>

        <div class="flex justify-between mt-8 gap-2">
          <Button variant="outline" type="button" class="w-full h-11">
            <RouterLink to="/">{{ $t('import.cancelButton') }}</RouterLink>
          </Button>
          <Button type="submit" :disabled="loading" class="w-full h-11">
            <Loader2Icon v-if="loading" class="mr-2 h-4 w-4 animate-spin" />
            {{ $t('import.importButton') }}
          </Button>
        </div>
      </form>
    </div>
  </LayoutScroll>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useToast } from '@/components/ui/toast'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import * as z from 'zod'
import LayoutScroll from '@/components/layout/LayoutScroll.vue'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Button } from '@/components/ui/button'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import {
  Form,
  FormField,
  FormItem,
  FormLabel,
  FormControl,
  FormDescription,
  FormMessage,
} from '@/components/ui/form'
import { useWalletStore } from '@/store'
import { passwordSchema, mnemonicSchema, privateKeySchema } from '@/utils/validation'
import { hashPassword } from '@/utils/crypto'
import {KeyRound, Loader2Icon} from 'lucide-vue-next'

const { toast } = useToast()
const router = useRouter()
const walletStore = useWalletStore()
const tab = ref<any>('mnemonic')
const loading = ref(false)

const showToast = (variant: 'default' | 'destructive', title: string, description: string | Error) => {
  toast({
    variant,
    title,
    description: typeof description === 'string' ? description : description.message
  })
}

const formSchema = toTypedSchema(
  z
    .object({
      mnemonic: mnemonicSchema.optional(),
      privateKey: privateKeySchema.optional(),
      password: passwordSchema,
      confirmPassword: z.string().min(1, 'Please confirm your password'),
    })
    .refine((data) => data.password === data.confirmPassword, {
      message: "Passwords do not match",
      path: ['confirmPassword'],
    })
    .refine(
      (data) => {
        if (data.mnemonic && data.privateKey) return false
        return data.mnemonic || data.privateKey
      },
      {
        message: 'Please provide either recovery phrase or private key',
        path: ['mnemonic'],
      }
    )
)

const form = useForm({
  validationSchema: formSchema,
})

const onSubmit = form.handleSubmit(async (values) => {
  console.log('Form values:', values)

  loading.value = true

  const hashedPassword = await hashPassword(values.password)

  if (tab.value === 'mnemonic') {
    if (values.mnemonic) {
      const [err, result] = await walletStore.importWallet(
        values.mnemonic,
        hashedPassword
      )
      if (err) {
        showToast('destructive', 'Error', err)
        loading.value = false
        return
      }

      showToast('default', 'Success', 'Wallet imported successfully')
      router.push('/wallet')
    }
  }

  loading.value = false
})
</script>
