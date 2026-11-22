<template>
    <div class="relative">
      <Button
        variant="ghost"
        class="size-11 hover:bg-muted/60"
        :aria-label="$t('subWalletManager.title')"
        @click="navigateToSubWalletManager"
      >
        <Icon icon="lucide:chevron-down" aria-hidden="true" class="size-5 text-muted-foreground" />
      </Button>
    </div>
  </template>

  <script setup lang="ts">
  import { useRouter } from 'vue-router'
  import { Icon } from '@iconify/vue'
  import { Button } from '@/components/ui/button'

  const router = useRouter()
  const navigateToSubWalletManager = () => {
    router.push('/wallet/sub-wallet-manager')
  }
  </script>
