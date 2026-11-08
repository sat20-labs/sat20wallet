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
      <Button size="sm" @click="open">
        {{ accountState.hasQuotaIssue ? t(accountState.quotaActionKey) : state?.recovery_configured ? t('accountManagement.ui.manage') : state?.activation_pending ? t('accountManagement.ui.continueActivation') : state?.local_only ? t('accountManagement.ui.activate') : t('accountManagement.ui.viewStatus') }}
      </Button>
    </div>
    <div v-if="state && (state.active || state.account_state === 'local' || accountState.quotaNoticeKey)" class="rounded-md border p-3 text-xs text-muted-foreground space-y-1">
      <div>{{ t('accountManagement.ui.accountStatus', { value: t(`accountManagement.accountState.${state.account_state}`) }) }}</div>
      <div v-if="accountState.hasDatabaseIssue" role="alert">{{ t('accountManagement.readiness.databaseFailed') }}</div>
      <div v-else-if="state.account_state === 'local'" role="status">{{ t('accountManagement.readiness.local') }}</div>
      <div v-else-if="state.activation_pending" role="status">{{ t('accountManagement.ui.activationPendingNotice') }}</div>
      <div v-else-if="state.account_state === 'syncing'" role="status">{{ t('accountManagement.readiness.syncing') }}</div>
      <div v-else-if="state.account_state === 'error'" role="alert">{{ t('accountManagement.readiness.error') }}</div>
      <div v-else-if="['pending', 'uploading'].includes(state.backup_state)" role="status">{{ t(state.backup_state === 'pending' ? 'accountManagement.readiness.backupPending' : 'accountManagement.readiness.backupUploading') }}</div>
      <div v-else-if="state.backup_state === 'cleanup'" role="status">{{ t('accountManagement.readiness.cleanup') }}</div>
      <div v-else>{{ t('accountManagement.ui.status', { value: state.storage_mode === 'paid' ? t('accountManagement.ui.paidStorage') : t('accountManagement.ui.temporaryStorage') }) }}</div>
      <div v-if="state.recovery_configured">{{ t('accountManagement.ui.recoveryMode', { value: state.recovery_mode === '2of3' ? t('accountManagement.ui.modeConvenient') : t('accountManagement.ui.modeSecure') }) }}</div>
      <div v-if="state.last_rehearsal_at">{{ t('accountManagement.ui.lastRehearsal', { value: new Date(state.last_rehearsal_at).toLocaleString() }) }}</div>
      <div>{{ t('accountManagement.ui.pendingChanges', { value: state.pending_changes || 0 }) }}</div>
      <div v-if="state.managed_data_dirty && state.recovery_configured" class="text-amber-500">{{ t('accountManagement.ui.unbackedNotice') }}</div>
      <div v-if="state.recovery_configured">{{ t('accountManagement.dkvsStatusLabel', { status: t(`accountManagement.dkvsSyncStatus.${state.dkvs_status || 'unknown'}`) }) }}</div>
      <div v-if="state.recovery_configured">{{ t('accountManagement.dkvsWriteLabel', { status: t(state.dkvs_write_allowed === true ? 'accountManagement.dkvsWriteAllowed' : state.dkvs_write_allowed === false ? 'accountManagement.dkvsReadOnly' : 'accountManagement.dkvsWriteUnknown') }) }}</div>
      <p v-if="accountState.quotaNoticeKey" role="alert" class="text-amber-500">
        {{ t(accountState.quotaNoticeKey) }}
      </p>
      <div v-if="state.dkvs_cache_at_ms">
        {{ t('accountManagement.dkvsCacheVerifiedAt', { time: new Date(state.dkvs_cache_at_ms).toLocaleString() }) }}<span v-if="state.dkvs_cache_height !== undefined">{{ t('accountManagement.dkvsCacheHeight', { height: state.dkvs_cache_height }) }}</span>
      </div>
      <div>{{ t('accountManagement.dkvsPendingRequestsLabel', { count: state.dkvs_pending_requests ?? 0 }) }}</div>
      <div
        v-if="state.last_dkvs_sync_error"
        class="mt-2 rounded-md border border-red-500/40 bg-red-500/10 p-2 text-red-400"
      >
        <div class="font-medium">{{ t('accountManagement.ui.lastDiagnostic', { value: state.last_dkvs_sync_error_code || 'DKVS_SYNC_ERROR' }) }}</div>
        <div class="mt-1 break-words">{{ accountDisplayMessage(t, state.last_dkvs_sync_error) }}</div>
        <div v-if="state.last_dkvs_sync_error_at" class="mt-1 text-red-400/80">
          {{ t('accountManagement.ui.lastFailure', { value: new Date(state.last_dkvs_sync_error_at).toLocaleString() }) }}
        </div>
      </div>
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
