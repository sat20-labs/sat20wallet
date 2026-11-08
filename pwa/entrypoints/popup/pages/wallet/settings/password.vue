<template>
  <LayoutSecond :title="t('settingsUi.passwordTitle')">
    <form @submit.prevent="onSubmit">
      <FormField name="oldPassword" v-slot="{ componentField }">
        <FormItem>
          <FormLabel for="oldPassword">{{ t('settingsUi.oldPassword') }}</FormLabel>
          <FormControl>
            <Input id="oldPassword" type="password" autocomplete="current-password" v-bind="componentField"
              :aria-label="t('settingsUi.oldPassword')" aria-required="true" />
          </FormControl>
          <FormMessage />
        </FormItem>
      </FormField>
      <FormField name="newPassword" v-slot="{ componentField }">
        <FormItem class="mt-4">
          <FormLabel for="newPassword">{{ t('settingsUi.newPassword') }}</FormLabel>
          <FormControl>
            <Input id="newPassword" type="password" autocomplete="new-password" v-bind="componentField" :aria-label="t('settingsUi.newPassword')"
              aria-required="true" />
          </FormControl>
          <FormMessage />
        </FormItem>
      </FormField>
      <FormField name="confirmPassword" v-slot="{ componentField }">
        <FormItem class="mt-4">
          <FormLabel for="confirmPassword">{{ t('settingsUi.confirmPassword') }}</FormLabel>
          <FormControl>
            <Input id="confirmPassword" type="password" autocomplete="new-password" v-bind="componentField"
              :aria-label="t('settingsUi.confirmPassword')" aria-required="true" />
          </FormControl>
          <FormMessage />
        </FormItem>
      </FormField>
      <div class="mt-6 flex justify-end">
        <Button class="w-full" :disabled="isLoading" type="submit" :aria-label="t('settingsUi.confirmChange')">
          <span v-if="isLoading">{{ t('settingsUi.changing') }}</span>
          <span v-else>{{ t('settingsUi.confirmChange') }}</span>
        </Button>
      </div>
    </form>
  </LayoutSecond>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
const { t } = useI18n()
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useWalletStore } from '@/store/wallet'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import * as z from 'zod'
import {
  FormField,
  FormItem,
  FormLabel,
  FormControl,
  FormMessage
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { useToast } from '@/components/ui/toast-new'
import walletManager from '@/utils/sat20'
import { biometricCredentialManager } from '@/utils/biometricCredentials'

const formSchema = computed(() => toTypedSchema(z.object({
  oldPassword: z.string().min(1, t('settingsUi.enterOldPassword')),
  newPassword: z.string().min(6, t('settingsUi.passwordLength')),
  confirmPassword: z.string().min(1, t('settingsUi.enterConfirmation')),
}).refine(data => data.newPassword === data.confirmPassword, {
  message: t('settingsUi.passwordMismatch'),
  path: ['confirmPassword']
})))

const { handleSubmit, resetForm } = useForm({
  validationSchema: formSchema
})

const walletStore = useWalletStore()
const router = useRouter()
const { toast } = useToast()
const isLoading = ref(false)

const onSubmit = handleSubmit(async (values) => {
  isLoading.value = true
  try {
		const [err] = await walletManager.changePassword(values.oldPassword, values.newPassword)
    if (err) {
      toast({ title: t('settingsUi.changeFailed'), description: err.message || t('settingsUi.checkPassword'), variant: 'destructive' })
      return
    }
    const cleared = await biometricCredentialManager.clearAllCredentials()
    toast({ title: t('settingsUi.changeSuccess'), description: cleared.success
      ? t('settingsUi.passwordUpdated')
      : t('settingsUi.biometricClearFailed', { error: cleared.error }),
      variant: cleared.success ? 'success' : 'info' })
    await router.replace('/wallet/setting')
  } catch (err: any) {
    toast({ title: t('settingsUi.changeFailed'), description: err?.message || t('settingsUi.checkPassword'), variant: 'destructive' })
  } finally {
    resetForm()
    isLoading.value = false
  }
})
</script>

<style scoped>
.bg-card {
  background: var(--card);
}
</style>
