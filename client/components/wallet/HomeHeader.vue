<template>
  <header class="flex justify-between items-center mb-6 gap-4">
    <!-- 左侧钱包选择 -->
    <WalletSelector @wallet-changed="handleWalletChange" @wallet-created="handleWalletCreated" />

    <!-- 右侧功能按钮 -->
    <div class="flex gap-2 items-center">
      <BtcFeeSelect @change="walletStore.setFeeRate" />
      <NetworkSelect />

      <!-- TranscendingMode 图标按钮 -->
      <Button size="xs" variant="ghost" @click="showTranscendingMode = true" 
      class="flex justify-center items-center *:w-7 h-7  text-foreground/80 bg-zinc-800 border border-zinc-800 rounded-lg">
        <Icon :icon="currentIcon" class="w-4 h-4" />
        <ChevronDown class="h-4 w-4" />
      </Button>
    </div>

    <!-- TranscendingMode 弹出窗口 -->
    <div
      v-if="showTranscendingMode"
      class="fixed inset-0 bg-black/50 flex items-center justify-center z-50"
    >
      <div class="bg-zinc-900 rounded-lg shadow-lg w-[330px] p-4 relative border border-zinc-700">
        <!-- 关闭按钮 -->
        <button
          class="absolute top-2 right-2 text-gray-500 hover:text-gray-700"
          @click="showTranscendingMode = false"
        >
          <Icon icon="lucide:x" class="w-5 h-5" />
        </button>

        <!-- TranscendingMode 组件 -->
        <TranscendingMode
          v-model:selectedType="selectedType"
          :l1-assets="l1Assets"
          @splicing_in="handleSplicingIn"
          @send="handleSend"
          @deposit="handleDeposit"
          @close="showTranscendingMode = false"
        />
      </div>
    </div>
  </header>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { Icon } from '@iconify/vue'
import BtcFeeSelect from '@/components/common/BtcFeeSelect.vue'
import NetworkSelect from '@/components/common/NetworkSelect.vue'
import WalletSelector from './WalletSelector.vue'
import { ChevronDown } from 'lucide-vue-next'
import TranscendingMode from '@/components/wallet/TranscendingMode.vue'
import { useTranscendingModeStore } from '@/store'
import { Button } from '@/components/ui/button'

import { useWalletStore } from '@/store'

// 状态管理
const walletStore = useWalletStore()
const showTranscendingMode = ref(false) // 控制弹出窗口显示状态
const transcendingModeStore = useTranscendingModeStore()
const selectedType = ref('ORDX') // 示例：当前选择的资产类型
const l1Assets = computed(() => {
  // 示例：L1 资产数据
  return []
})

// 动态图标
const currentIcon = computed(() => {
  //console.log('Header Selected Transcending Mode:', transcendingModeStore.selectedTranscendingMode) // Uncommenting the console log
  return transcendingModeStore.selectedTranscendingMode === 'poolswap' ? 'material-icon-theme:hurl' : 'material-icon-theme:supabase'
})

// 示例事件处理
const handleWalletChange = (wallet: any) => {
  console.log('Wallet changed:', wallet)
  // TODO: 实现钱包切换逻辑
}

const handleWalletCreated = (wallet: any) => {
  console.log('New wallet created:', wallet)
  // TODO: 实现新钱包创建后的逻辑
}

const handleSplicingIn = () => {
  console.log('Splicing in triggered')
  // TODO: 实现 Splicing In 的逻辑
}

const handleSend = () => {
  console.log('Send triggered')
  // TODO: 实现 Send 的逻辑
}

const handleDeposit = () => {
  console.log('Deposit triggered')
  // TODO: 实现 Deposit 的逻辑
}
</script>
