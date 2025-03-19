<template>
  <LayoutScroll>
    <div class="p-1">
      <h1 class="text-2xl font-bold mb-2">Import Wallet</h1>
      <p class="text-muted-foreground mb-4">
        Import your existing wallet using recovery phrase or private key
      </p>

      <form @submit="onSubmit">
        <Tabs v-model="tab" class="space-y-4">
          <TabsList class="grid w-full grid-cols-2">
            <TabsTrigger value="mnemonic">Recovery Phrase</TabsTrigger>
            <!-- <TabsTrigger value="private-key">Private Key</TabsTrigger> -->
          </TabsList>

          <TabsContent value="mnemonic">
            <Alert>
              <AlertDescription>
                Enter your 12 or 24-word recovery phrase in the correct order
              </AlertDescription>
            </Alert>

            <FormField v-slot="{ componentField }" name="mnemonic">
              <FormItem>
                <FormLabel>Recovery Phrase</FormLabel>
                <FormControl>
                  <Textarea
                    v-bind="componentField"
                    placeholder="Enter your recovery phrase, words separated by spaces"
                    rows="3"
                  />
                </FormControl>
                <FormDescription>
                  Usually 12 or 24 words long, separated by single spaces
                </FormDescription>
                <FormMessage />
              </FormItem>
            </FormField>
          </TabsContent>

          <TabsContent value="private-key">
            <Alert>
              <KeyRound class="h-4 w-4" />
              <AlertDescription>
                Enter your wallet's private key in WIF or hexadecimal format
              </AlertDescription>
            </Alert>

            <FormField v-slot="{ componentField }" name="privateKey">
              <FormItem>
                <FormLabel>Private Key</FormLabel>
                <FormControl>
                  <Input
                    type="password"
                    v-bind="componentField"
                    placeholder="Enter your private key"
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            </FormField>
          </TabsContent>

          <div class="space-y-4 pt-4 border-t">
            <FormField v-slot="{ componentField }" name="password">
              <FormItem>
                <FormLabel>New Wallet Password</FormLabel>
                <FormControl>
                  <Input
                    type="password"
                    v-bind="componentField"
                    placeholder="Enter password"
                  />
                </FormControl>
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
        </Tabs>

        <div class="flex justify-between mt-4">
          <Button variant="outline" type="button">
            <RouterLink to="/"> Cancel </RouterLink>
          </Button>
          <Button type="submit" :disabled="loading">
            <Loader2Icon v-if="loading" class="mr-2 h-4 w-4 animate-spin" />
            Import Wallet
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
