<template>
  <header class="flex justify-between items-center mb-6 gap-4">
    <div></div>
    <!-- <Button variant="outline" class="">
      {{ walletName }}
    </Button> -->
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
