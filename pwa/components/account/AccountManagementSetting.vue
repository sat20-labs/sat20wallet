<template>
  <div class="space-y-3 px-4 py-2">
    <div class="flex items-start justify-between gap-3">
      <div class="space-y-1">
        <div class="flex items-center gap-2 font-medium">
          <Icon icon="lucide:shield-check" class="h-5 w-5 text-green-500" />
          自托管账户恢复
        </div>
        <p class="text-sm text-muted-foreground">
          将多个钱包的核心账户数据加密保存到 DKVS，并在新设备上一次性恢复。
        </p>
      </div>
      <Button size="sm" @click="open">
        {{ state ? '管理' : '激活' }}
      </Button>
    </div>
    <div v-if="state" class="rounded-md border p-3 text-xs text-muted-foreground space-y-1">
      <div>状态：{{ state.status === 'active-paid' ? '付费保存' : '临时缓存' }}</div>
      <div>恢复模式：{{ state.recoveryMode === '2of3' ? '2/3 便捷恢复' : '2/2 增强安全' }}</div>
      <div>上次演练：{{ new Date(state.lastRehearsalAt).toLocaleString() }}</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { Icon } from '@iconify/vue'
import { Button } from '@/components/ui/button'
import { loadAccountManagementState, type AccountManagementState } from '@/lib/account-management-state'

const router = useRouter()
const state = ref<AccountManagementState | null>(null)

onMounted(() => {
  state.value = loadAccountManagementState()
})

const open = () => router.push('/wallet/setting/account-management')
</script>
