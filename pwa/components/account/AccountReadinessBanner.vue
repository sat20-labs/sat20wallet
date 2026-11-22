<template>
  <aside
    v-if="store.shouldShowBanner"
    class="rounded-lg border bg-muted/30 p-3 text-foreground"
    :class="store.readinessError ? 'border-destructive/60' : 'border-border'"
    :role="store.readinessError ? 'alert' : 'status'"
    aria-live="polite"
  >
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="min-w-0 space-y-1 text-sm">
        <p>{{ t(store.readinessKey) }}</p>
        <p v-if="store.retryQueued" class="text-xs text-muted-foreground">
          {{ t('accountManagement.readiness.retryQueued') }}
        </p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <Button v-if="store.discoveryIssue" size="sm" variant="outline" @click="router.push('/restore-account')">
          {{ t('accountManagement.readiness.restoreAccount') }}
        </Button>
        <Button v-if="canRetry" size="sm" variant="outline" :disabled="store.retrying" @click="store.retryNow">
          {{ store.retrying ? t('accountManagement.readiness.retrying') : t('accountManagement.readiness.retryNow') }}
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
const canRetry = computed(() => !store.hasDatabaseIssue && (store.statusReadFailed || store.canRetryBackup && (store.retryFailed || state.value?.state === 'error' ||
  state.value?.backup_state === 'pending' || state.value?.backup_state === 'uploading' ||
  state.value?.backup_state === 'cleanup' || store.moduleIssues.length > 0 ||
  store.hasQuotaIssue)))

</script>
