<template>
  <div class="space-y-3 px-4 py-2">
    <div class="flex items-start justify-between gap-3">
      <div class="space-y-1">
        <div class="flex items-center gap-2 font-medium">
          <Icon icon="lucide:shield-check" class="h-5 w-5 text-green-500" />
          {{ t('accountManagement.ui.title') }}
        </div>
        <p class="text-sm text-muted-foreground">
          {{ t('accountManagement.ui.intro') }}
        </p>
      </div>
      <Button size="sm" @click="open">{{ t('accountManagement.ui.manage') }}</Button>
    </div>
    <div v-if="state" class="space-y-1 rounded border p-3 text-xs text-muted-foreground">
      <p>{{ t('accountManagement.ui.accountStatus', {value: t(`accountManagement.accountState.${state.state}`)}) }}</p>
      <p>{{ t('accountManagement.ui.backupState', {value: t(`accountManagement.backupState.${state.backup_state}`)}) }}</p>
      <p v-if="state.storage">{{ t(state.storage.option.id === 'paid' ? 'accountManagement.ui.paidStorage' : 'accountManagement.ui.temporaryStorage') }}</p>
      <p v-if="state.recovery_mode">{{ t('accountManagement.ui.recoveryMode', {value: state.recovery_mode}) }}</p>
      <p v-if="state.last_rehearsal_at">{{ t('accountManagement.ui.lastRehearsal', {value: new Date(state.last_rehearsal_at).toLocaleString()}) }}</p>
      <p v-if="state.error_code" role="alert">{{ state.error_code }}</p>
      <p v-if="accountState.quotaNoticeKey" role="alert">{{ t(accountState.quotaNoticeKey) }}</p>
    </div>
    <p v-if="statusReadError" role="status" class="text-xs text-destructive">{{ t(statusReadError) }}</p>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { computed } from 'vue'
import { storeToRefs } from 'pinia'
import { useRouter } from 'vue-router'
import { Icon } from '@iconify/vue'
import { Button } from '@/components/ui/button'
import { accountDisplayMessage } from '@/utils/accountManagement'
import { useAccountManagementStore } from '@/store/accountManagement'

const { t } = useI18n()
const router = useRouter()
const accountState = useAccountManagementStore()
const { status: state } = storeToRefs(accountState)
const statusReadError = computed(() => accountState.statusReadFailed ? 'accountManagement.dkvsStatusReadFailed' : '')

const open = () => router.push('/wallet/setting/account-management')
</script>
