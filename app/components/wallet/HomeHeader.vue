<template>
  <header class="flex justify-between items-center mb-6 gap-2">
    <!-- 左侧钱包选择 -->
    <WalletSelector @wallet-changed="handleWalletChange" @wallet-created="handleWalletCreated" />

    <!-- 右侧功能按钮 -->
    <div class="flex items-center">
      <BtcFeeSelect @change="walletStore.setBtcFeeRate" />
      <SatsnetFeeSelect @change="walletStore.setSatsnetFeeRate" />
      <NetworkSelect />
    </div>

    </header>
</template>

<script setup lang="ts">
import BtcFeeSelect from '@/components/common/BtcFeeSelect.vue'
import SatsnetFeeSelect from '@/components/common/SatsnetFeeSelect.vue'
import NetworkSelect from '@/components/common/NetworkSelect.vue'
import WalletSelector from './WalletSelector.vue'
import { useWalletStore } from '@/store'

// 状态管理
const walletStore = useWalletStore()

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
