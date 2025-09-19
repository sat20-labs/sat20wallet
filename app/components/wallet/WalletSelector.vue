<template>
  <div class="relative">
   <Button
       @click="navigateToWalletManager"
      variant="outline"
      size="xs"
      class="rounded-md text-gray-300 text-xs font-normal ml-2"
    >
      <img src="@/assets/sat20-logo.svg" alt="ORDX" class="w-4 h-4 shrink-0" />
      <span class="text-xs font-medium text-muted-foreground/80">{{ wallet?.name }}</span>
      <Icon icon="lucide:chevron-down" class="w-4 h-4 text-foreground/60" />
    </Button>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { Icon } from '@iconify/vue'
import { Button } from '@/components/ui/button'
import { useWalletStore } from '@/store'

const router = useRouter()
const walletStore = useWalletStore()
const { wallet } = storeToRefs(walletStore)
console.log(wallet);

// Methods
const navigateToWalletManager = () => {
  router.push('/wallet/manager')
}
</script>

<style scoped>
.rotate-180 {
  transform: rotate(180deg);
}
</style>
