<template>
  <div class="layout-container h-full flex flex-col w-full safe-area-top">
    <div class="flex-1 w-full overflow-hidden">
      <ScrollArea class="h-full p-4">
        <slot />
      </ScrollArea>
    </div>
    <NavFooter />
  </div>
</template>

<script setup lang="ts">
import { ScrollArea } from '@/components/ui/scroll-area'
import NavFooter from '@/components/layout/NavFooter.vue'
</script>

<style scoped>
.safe-area-top {
  /* 固定边距作为后备方案 (约24px，适合大多数Android设备) */
  padding-top: 24px;

  /* 如果支持环境变量，则使用环境变量覆盖固定值 */
  padding-top: env(safe-area-inset-top, 24px);

  /* 确保在不同设备上都有最小边距 */
  min-height: calc(100vh - 24px);
}

/* 针对特定设备的媒体查询 */
@media screen and (min-height: 800px) {
  .safe-area-top {
    padding-top: 32px;
    padding-top: env(safe-area-inset-top, 32px);
  }
}

@media screen and (max-width: 380px) {
  .safe-area-top {
    padding-top: 20px;
    padding-top: env(safe-area-inset-top, 20px);
  }
}
</style>

