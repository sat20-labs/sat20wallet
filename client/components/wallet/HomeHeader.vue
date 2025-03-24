<template>
  <header class="flex justify-between items-center mb-6 gap-4">
    <WalletSelector @wallet-changed="handleWalletChange" @wallet-created="handleWalletCreated" />
    <div class="flex gap-2">
      <BtcFeeSelect @change="walletStore.setFeeRate" />
      <NetworkSelect />
    </div>
  </header>
</template>

<script setup lang="ts">
import BtcFeeSelect from '@/components/common/BtcFeeSelect.vue'
import NetworkSelect from '@/components/common/NetworkSelect.vue'
import WalletSelector from './WalletSelector.vue'
import { useWalletStore } from '@/store'

const walletStore = useWalletStore()

// 处理钱包切换
const handleWalletChange = (wallet: any) => {
  console.log('Wallet changed:', wallet)
  // TODO: 实现钱包切换逻辑
}

// 处理新钱包创建
const handleWalletCreated = (wallet: any) => {
  console.log('New wallet created:', wallet)
  // TODO: 实现新钱包创建后的逻辑
}
</script>
