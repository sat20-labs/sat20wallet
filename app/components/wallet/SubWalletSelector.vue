<template>
    <div class="relative">
      <Button
        variant="ghost"
        class="flex items-center gap-2 px-2 hover:bg-muted/60"
        @click="navigateToSubWalletManager"
      >
        <Icon icon="lucide:chevron-down" class="w-6 h-6 shrink-0 text-foreground/60" />
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
  
  // 计算当前钱包名称
  const walletName = computed(() => {
    const index = walletStore.accountIndex || 0
    return `Account ${index + 1}`
  })
  
  // Methods
  const navigateToSubWalletManager = () => {
    router.push('/wallet/sub-wallet-manager')
  }
  </script>
  
  <style scoped>
  .rotate-180 {
    transform: rotate(180deg);
  }
  </style>
  