<template>
  <header class="flex justify-between items-center mb-6 gap-4">
    <div class="flex items-center gap-2">
      <img src="@/assets/sat20-logo.svg" alt="SAT20" class="w-6 h-6" />
      <span class="text-lg font-bold">SAT20</span>
    </div>
    <div class="flex gap-2">
      <BtcFeeSelect @change="walletStore.setFeeRate" />
      <NetworkSelect />
    </div>
  </header>
</template>

<script setup lang="ts">
// import { Bitcoin, ChevronDown, Layers } from 'lucide-vue-next'
import BtcFeeSelect from '@/components/common/BtcFeeSelect.vue'
import NetworkSelect from '@/components/common/NetworkSelect.vue'
import { Button } from '@/components/ui/button'
import { useWalletStore } from '@/store'

defineProps({
  walletName: {
    type: String,
    default: 'HD Wallet #2',
  },
  networkId: {
    type: String,
    default: '1',
  },
  currency: {
    type: String,
    default: 'Bitcoin',
  },
})
const walletStore = useWalletStore()
</script>
