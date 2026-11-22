<template>
  <header class="mb-5 flex flex-wrap items-center justify-between gap-2">
    <!-- 左侧钱包选择 -->
    <div class="flex min-w-0 items-center gap-2">
      <WalletSelector @wallet-changed="handleWalletChange" @wallet-created="handleWalletCreated" />
      <RouterLink
        v-if="showSyncStatus && syncStatus"
        to="/wallet/setting/account-management"
        class="inline-flex min-h-10 shrink-0 items-center rounded px-1 text-xs font-medium hover:underline"
        :class="syncStatus === 'error' ? 'text-destructive' : 'text-muted-foreground'"
        :aria-label="$t('accountManagement.syncLabel', { status: $t(`accountManagement.syncBadge.${syncStatus}`) })"
      >
        {{ $t(`accountManagement.syncBadge.${syncStatus}`) }}
      </RouterLink>
    </div>

    <!-- 右侧功能按钮 -->
    <div class="flex flex-wrap items-center gap-1.5">
      <BtcFeeSelect @change="walletStore.setBtcFeeRate" />
      <SatsnetFeeSelect @change="walletStore.setSatsnetFeeRate" />
      <NetworkSelect />
      <Button
        size="sm"
        variant="outline"
        class="min-h-10 min-w-10 gap-1 px-2"
        :aria-label="$t('pwaUi.design.transcendMode')"
        @click="showTranscendingMode = true"
      >
        <Icon :icon="currentIcon" class="h-4 w-4" />
        <ChevronDown class="h-4 w-4" />
      </Button>
    </div>

    <div
      v-if="showTranscendingMode"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
    >
      <div class="relative w-[calc(100%_-_2rem)] max-w-sm rounded-xl border border-border bg-card p-4 shadow-lg">
        <button
          type="button" :aria-label="$t('pwaUi.common.close')"
          class="absolute right-2 top-2 grid size-10 place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"
          @click="showTranscendingMode = false"
        >
          <Icon icon="lucide:x" class="h-5 w-5" />
        </button>
        <TranscendingMode @close="showTranscendingMode = false" />
      </div>
    </div>
  </header>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { storeToRefs } from 'pinia'
import { Icon } from '@iconify/vue'
import BtcFeeSelect from '@/components/common/BtcFeeSelect.vue'
import SatsnetFeeSelect from '@/components/common/SatsnetFeeSelect.vue'
import NetworkSelect from '@/components/common/NetworkSelect.vue'
import WalletSelector from './WalletSelector.vue'
import TranscendingMode from '@/components/wallet/TranscendingMode.vue'
import { ChevronDown } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { useTranscendingModeStore, useWalletStore } from '@/store'
import { useAccountManagementStore } from '@/store/accountManagement'

// 状态管理
defineProps<{ showSyncStatus?: boolean }>()
const walletStore = useWalletStore()
const account = useAccountManagementStore()
const transcendingModeStore = useTranscendingModeStore()
const { selectedTranscendingMode } = storeToRefs(transcendingModeStore)
const showTranscendingMode = ref(false)
const syncStatus = computed(() => {
  if (!account.sessionReady) return ''
  const state = account.status?.state
  const backup = account.status?.backup_state
  if (account.readinessError || account.moduleIssues.length || backup === 'error' || backup === 'conflict') return 'error'
  if (state === 'local' || state === 'locked') return state
  if (state === 'ready' && backup === 'backed_up') return 'synced'
  if (state || account.loading) return 'syncing'
  return ''
})

const currentIcon = computed(() => {
  return selectedTranscendingMode.value === 'poolswap'
    ? 'material-icon-theme:hurl'
    : 'material-icon-theme:supabase'
})

// 事件处理
const handleWalletChange = (wallet: any) => {
  console.log('Wallet changed:', wallet)
  // TODO: 实现钱包切换逻辑
}

const handleWalletCreated = (wallet: any) => {
  console.log('New wallet created:', wallet)
  // TODO: 实现新钱包创建后的逻辑
}
</script>
