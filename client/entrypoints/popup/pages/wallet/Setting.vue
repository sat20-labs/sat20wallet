<template>
  <LayoutHome>
    <!-- <SecuritySetting />
    <NetworkSetting /> -->
    <div class="space-y-4">
      <Button as-child>
        <RouterLink to="/wallet/setting/phrase" class="w-full mt-2">
          Show Phrase
        </RouterLink>
      </Button>
      <DeleteWallet />
    </div>
    <!-- <AppearanceSetting /> -->
  </LayoutHome>
</template>

<script setup lang="ts">
import LayoutHome from '@/components/layout/LayoutHome.vue'
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import SecuritySetting from '@/components/setting/SecuritySetting.vue'
import DeleteWallet from '@/components/setting/DeleteWallet.vue'
import NetworkSetting from '@/components/setting/NetworkSetting.vue'
import AppearanceSetting from '@/components/setting/AppearanceSetting.vue'
import { Button } from '@/components/ui/button'
</script>
