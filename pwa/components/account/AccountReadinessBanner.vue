<template>
  <aside
    v-if="store.shouldShowBanner"
    class="mx-2 mt-2 max-w-2xl shrink-0 rounded-md border bg-background p-3 text-foreground shadow-sm"
    :class="isError ? 'border-destructive/60' : 'border-border'"
    :role="isError ? 'alert' : 'status'"
    aria-live="polite"
  >
    <div class="flex items-start justify-between gap-3">
      <div class="min-w-0 space-y-1 text-sm">
        <p>{{ message }}</p>
        <p v-if="store.retryQueued" class="text-xs text-muted-foreground">
          {{ t('accountManagement.readiness.retryQueued') }}
        </p>
      </div>
      <div class="flex shrink-0 items-center gap-2">
        <Button v-if="canRetry" size="sm" variant="outline" :disabled="store.retrying" @click="store.retryNow">
          {{ store.retrying ? t('accountManagement.readiness.retrying') : t('accountManagement.readiness.retryNow') }}
        </Button>
        <Button size="sm" variant="ghost" @click="openSettings">
          {{ store.hasQuotaIssue ? t(store.quotaActionKey) : t('accountManagement.readiness.manage') }}
        </Button>
      </div>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import { useAccountManagementStore } from '@/store/accountManagement'

const store = useAccountManagementStore()
const router = useRouter()
const { t } = useI18n()
const state = computed(() => store.status)
const isError = computed(() => store.statusReadFailed || store.retryFailed || state.value?.account_state === 'error' ||
  store.hasQuotaIssue)
const canRetry = computed(() => !store.hasDatabaseIssue && (store.statusReadFailed || store.retryFailed || state.value?.account_state === 'error' ||
  state.value?.backup_state === 'pending' || state.value?.backup_state === 'uploading' ||
  state.value?.backup_state === 'cleanup' || store.moduleIssues.length > 0 ||
  store.hasQuotaIssue))

const message = computed(() => {
  if (store.hasDatabaseIssue) return t('accountManagement.readiness.databaseFailed')
  if (store.quotaNoticeKey) return t(store.quotaNoticeKey)
  if (store.statusReadFailed) return t('accountManagement.readiness.readFailed')
  if (store.retryFailed) return t('accountManagement.readiness.retryFailed')
  if (store.loading && !state.value) return t('accountManagement.readiness.syncing')
  switch (state.value?.account_state) {
    case 'local': return t('accountManagement.readiness.local')
    case 'locked': return t('accountManagement.readiness.locked')
    case 'syncing': return t('accountManagement.readiness.syncing')
    case 'error': return t('accountManagement.readiness.error')
    case 'ready':
      if (store.moduleIssues.length) return t('accountManagement.readiness.moduleIssue')
      if (state.value.backup_state === 'pending') return t('accountManagement.readiness.backupPending')
      if (state.value.backup_state === 'uploading') return t('accountManagement.readiness.backupUploading')
      if (state.value.backup_state === 'cleanup') return t('accountManagement.readiness.cleanup')
      return ''
    default: return ''
  }
})

const openSettings = () => router.push('/wallet/setting/account-management')
</script>
