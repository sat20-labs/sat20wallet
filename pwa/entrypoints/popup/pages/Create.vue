<template>
  <div class="pt-10 p-4">
    <div class="mb-4 text-center">
      <div class="flex flex-col items-center justify-center gap-2 mb-4">
        <img src="@/assets/sat20-logo.svg" alt="ORDX" class="w-14 h-14 mb-2" />
        <!-- <h1 class="text-2xl font-semibold">ORDX Wallet</h1> -->
      </div>
      <h1 class="text-2xl font-bold">
        {{ step === 1 ? $t('create.step1Title') : $t('create.step2Title') }}
      </h1>
      <p class="text-muted-foreground">
        {{
          step === 1
            ? $t('create.step1Subtitle')
            : $t('create.step2Subtitle')
        }}
      </p>
    </div>
    <form @submit.prevent="onSubmit">
      <div v-if="step === 1" class="space-y-4">
        <FormField v-slot="{ componentField }" name="password">
          <FormItem>
            <FormLabel>{{ $t('create.passwordLabel') }}</FormLabel>
            <FormControl>
              <Input
                type="password"
                v-bind="componentField"
                :placeholder="$t('create.passwordPlaceholder')"
              />
            </FormControl>
            <FormDescription>
              {{ $t('create.passwordDescription') }}
            </FormDescription>
            <FormMessage />
          </FormItem>
        </FormField>

        <FormField v-slot="{ componentField }" name="confirmPassword">
          <FormItem>
            <FormLabel>{{ $t('create.confirmPasswordLabel') }}</FormLabel>
            <FormControl>
              <Input
                type="password"
                v-bind="componentField"
                :placeholder="$t('create.confirmPasswordPlaceholder')"
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
            {{ $t('create.recoveryPhraseWarning') }}
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
            {{ $t('create.copyButton') }}
          </Button>
        </div>
      </div>

      <div class="flex justify-between mt-16 gap-2">
        <Button variant="outline" type="button" class="w-full">
          <RouterLink to="/">{{ $t('create.cancelButton') }}</RouterLink>
        </Button>
        <Button v-if="step === 1" type="submit" :disabled="loading" class="w-full">
          <Loader2Icon v-if="loading" class="mr-2 h-4 w-4 animate-spin" />
          {{ loading ? $t('create.creatingButton') : $t('create.continueButton') }}
        </Button>
        <Button v-else-if="committedReadError" type="button" @click="reloadCommittedWallet">{{ $t('pwaUi.create.reload') }}</Button>
        <Button v-else @click="handleConfirmSaved" as-child>
          <RouterLink to="/wallet">{{ $t('create.savedButton') }}</RouterLink>
        </Button>
      </div>
    </form>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
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
import { useToast } from '@/components/ui/toast-new'
import { Button } from '@/components/ui/button'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Input } from '@/components/ui/input'
import { useClipboard } from '@vueuse/core'
import { Eye, EyeOff, Copy, AlertTriangle, Loader2Icon } from 'lucide-vue-next'
import walletManager from '@/utils/sat20'
import { useWalletStore } from '@/store'
import { createPasswordSchema } from '@/utils/validation'

const { t } = useI18n()

const { toast } = useToast()
const loading = ref(false)
const step = ref(1)
const walletStore = useWalletStore()
const mnemonic = ref('')
const committedReadError = ref(false)
const reloadCommittedWallet = () => { window.location.hash = '#/unlock'; window.location.reload() }
const showMnemonic = ref(false)
const { copy, isSupported } = useClipboard()

const formSchema = toTypedSchema(createPasswordSchema)

const form = useForm({
  validationSchema: formSchema,
})

const onSubmit = form.handleSubmit(async (values) => {
  if (loading.value) return

  loading.value = true
  try {
  const [err, result] = await walletStore.createWallet(values.password)
  if (result) {
    committedReadError.value = Boolean(err)
    mnemonic.value = result as string
    step.value = 2
    // 添加创建成功的提示
    toast({
      variant: err ? 'info' : 'success',
      title: t('pwaUi.create.created'),
      description: err ? t('pwaUi.create.savedReload')
        : t('pwaUi.create.savePhrase'),
    })
    return
  }

  toast({
    variant: 'destructive',
    title: t('common.error'),
    description: err instanceof Error ? err.message : t('pwaUi.create.failed'),
  })
  } catch (error: any) {
    toast({ variant: 'destructive', title: t('common.error'), description: error?.message || t('pwaUi.create.failed') })
  } finally {
    loading.value = false
  }
})

const handleCopyMnemonic = async () => {
  if (!isSupported.value) {
    toast({
      variant: 'destructive',
      title: t('pwaUi.create.copyFailed'),
      description: t('pwaUi.create.clipboardUnsupported'),
    })
    return
  }

  await copy(mnemonic.value)
  toast({
    variant: 'success',
    title: t('pwaUi.create.phraseCopied'),
    description: t('pwaUi.create.phraseCopiedWarning'),
  })
}

const toggleShowMnemonic = () => {
  showMnemonic.value = !showMnemonic.value
}

const handleConfirmSaved = () => {
  toast({
    variant: 'info',
    title: t('pwaUi.create.proceeding'),
    description: t('pwaUi.create.welcome'),
  })
}
</script>
