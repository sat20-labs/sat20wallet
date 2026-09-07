<template>
  <div
    v-if="autopay && !autopay.ready"
    class="mb-3 flex items-start justify-between gap-3 rounded-lg border border-amber-500/50 bg-amber-500/10 p-3"
  >
    <div class="flex min-w-0 items-start gap-2">
      <Icon icon="lucide:triangle-alert" class="mt-0.5 h-4 w-4 shrink-0 text-amber-400" />
      <div class="min-w-0">
        <div class="text-sm font-medium text-amber-400">
          {{ autopay.can_fund ? 'AUTOPAY 需要充值' : 'AUTOPAY 状态异常' }}
        </div>
        <div class="mt-1 text-xs text-amber-400/90">
          {{ autopay.message || '账户付费同步当前不可用。' }}
        </div>
      </div>
    </div>
    <Button v-if="autopay.can_fund" size="sm" variant="outline" class="shrink-0" @click="openFunding">
      去充值
    </Button>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { Icon } from '@iconify/vue'
import { Button } from '@/components/ui/button'
import accountSDK, { type AccountAutopayFundingStatus } from '@/utils/accountManagement'

const router = useRouter()
const autopay = ref<AccountAutopayFundingStatus | null>(null)
let refreshTimer: ReturnType<typeof setInterval> | undefined

const openFunding = () => router.push('/wallet/setting/account-management')

const refresh = async () => {
  const account = await accountSDK.status()
  autopay.value = account.storage_mode === 'paid' ? await accountSDK.autopayStatus() : null
}

onMounted(() => {
  void refresh().catch(() => { autopay.value = null })
  refreshTimer = setInterval(() => void refresh().catch(() => undefined), 30_000)
})

onBeforeUnmount(() => {
  if (refreshTimer) clearInterval(refreshTimer)
})
</script>
