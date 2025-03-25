<template>
  <LayoutHome>
    <WalletHeader />

    <div class="space-y-4">
      <SecuritySetting />     
    </div>
    <div class="space-y-4 mt-4">
      <Button as-child class="h-12">
        <RouterLink to="/wallet/setting/phrase" class="w-full mt-2">
          <Icon icon="bi:file-earmark" class="w-4 h-4" /> Show Phrase
        </RouterLink>
      </Button>
      <DeleteWallet />
    </div>

    <!-- <AppearanceSetting /> -->

    <div class="fixed bottom-20 left-0 right-0 flex flex-col items-center gap-4">
      <div class="flex items-center gap-4">
        <!-- <a href="https://discord.gg/" target="_blank" class="text-muted-foreground hover:text-primary">
          <Icon icon="bi:discord" class="w-6 h-6" />
        </a> -->
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
      <div class="text-sm text-muted-foreground">Version: {{ version }}</div>
    </div>
  </LayoutHome>
</template>

<script setup lang="ts">
import LayoutHome from '@/components/layout/LayoutHome.vue'
import WalletHeader from '@/components/wallet/HomeHeader.vue'
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import SecuritySetting from '@/components/setting/SecuritySetting.vue'
import DeleteWallet from '@/components/setting/DeleteWallet.vue'
import NetworkSetting from '@/components/setting/NetworkSetting.vue'
import AppearanceSetting from '@/components/setting/AppearanceSetting.vue'
import { Button } from '@/components/ui/button'
import { version } from '@/package.json'
import { ref } from "vue";

const isOpen = ref(false);
</script>
