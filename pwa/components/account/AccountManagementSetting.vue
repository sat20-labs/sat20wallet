<template>
  <div class="space-y-3 px-4 py-2">
    <div class="flex items-start justify-between gap-3">
      <div class="space-y-1">
        <div class="flex items-center gap-2 font-medium">
          <Icon icon="lucide:shield-check" class="h-5 w-5 text-green-500" />
          自托管账户恢复
        </div>
        <p class="text-sm text-muted-foreground">
          加密保存多个钱包的核心账户数据，并在新设备上一次性恢复。
        </p>
      </div>
      <Button size="sm" @click="open">
        {{ state?.active ? '管理' : '激活' }}
      </Button>
    </div>
    <div v-if="state?.active" class="rounded-md border p-3 text-xs text-muted-foreground space-y-1">
      <div>状态：{{ state.storage_mode === 'paid' ? '付费保存' : '临时缓存' }}</div>
      <div>恢复模式：{{ state.recovery_mode === '2of3' ? '2/3 便捷恢复' : '2/2 增强安全' }}</div>
      <div v-if="state.last_rehearsal_at">上次演练：{{ new Date(state.last_rehearsal_at).toLocaleString() }}</div>
      <div>待同步变更：{{ state.pending_changes || 0 }}</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { Icon } from '@iconify/vue'
import { Button } from '@/components/ui/button'
import accountSDK from '@/utils/accountManagement'

const router = useRouter()
const state = ref<any>(null)

onMounted(async () => {
  try { state.value = await accountSDK.status() } catch { state.value = null }
})

const open = () => router.push('/wallet/setting/account-management')
</script>
