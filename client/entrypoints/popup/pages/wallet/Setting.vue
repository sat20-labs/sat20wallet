<template>
  <LayoutHome>
    <WalletHeader />
    <h2 class="px-4 text-2xl font-medium text-zinc-700/90">{{ $t('setting.title') }}</h2>
    <div class="space-y-2 py-4 px-0">
      <!-- Conditionally render EscapeHatch -->
      <EscapeHatch v-if="transcendingModeStore.selectedTranscendingMode !== 'poolswap'" />
      <Button class="w-full mt-2" @click="$router.push({ path: '/wallet/setting/utxo' })">
        {{$t('utxoManager.title')}}
      </Button>
      <Separator />
      <SecuritySetting />
      <Separator />
      <NetworkSetting />
      <Separator />
      <NodeSetting />
      <!-- <Separator />  
      <Separator />
      <Separator /> -->
    </div>

    <!-- <AppearanceSetting /> -->

    <div class="flex flex-col items-center gap-4 pb-4">
      <div class="flex items-center gap-4">
        <a href="https://twitter.com/sat20labs" target="_blank" class="text-muted-foreground hover:text-primary">
          <Icon icon="bi:twitter-x" class="w-4 h-4" />
        </a>
        <a href="https://github.com/sat20-labs/" target="_blank" class="text-muted-foreground hover:text-primary">
          <Icon icon="bi:github" class="w-4 h-4" />
        </a>
        <a href="https://t.me/ordxnals" target="_blank" class="text-muted-foreground hover:text-primary">
          <Icon icon="bi:telegram" class="w-4 h-4" />
        </a>
      </div>
      <div class="text-sm text-muted-foreground">{{ $t('setting.version') }}: {{ version }}</div>
    </div>
  </LayoutHome>
</template>

<script setup lang="ts">
import LayoutHome from '@/components/layout/LayoutHome.vue'
import WalletHeader from '@/components/wallet/HomeHeader.vue'
import SecuritySetting from '@/components/setting/SecuritySetting.vue'
import EscapeHatch from '@/components/setting/EscapeHatch.vue'
import NetworkSetting from '@/components/setting/NetworkSetting.vue'
import NodeSetting from '@/components/setting/NodeSetting.vue'

import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
import { version } from '@/package.json'
import { ref } from "vue";
import { useTranscendingModeStore } from '@/store'

type TranscendingMode = 'poolswap' | 'lightning'
const selectedTranscendingMode = ref<TranscendingMode>('poolswap')

const transcendingModeStore = useTranscendingModeStore()

const isOpen = ref(false);
</script>
