<template>
  <div class="layout-container h-full flex flex-col w-full safe-area-top">
    <div class="flex-1 w-full overflow-hidden">
      <ScrollArea class="h-full p-4">
        <slot />
      </ScrollArea>
    </div>
    <div class="safe-area-bottom">
      <NavFooter />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ScrollArea } from '@/components/ui/scroll-area'
import NavFooter from '@/components/layout/NavFooter.vue'
</script>

<style scoped>
.safe-area-top {
  /* 固定边距作为后备方案 */
  padding-top: 24px;

  /* iOS 11.0-11.2 使用 constant() */
  padding-top: constant(safe-area-inset-top);

  /* iOS 11.2+ 使用 env() */
  padding-top: env(safe-area-inset-top);

  /* 确保在不同设备上都有最小边距 */
  min-height: calc(100vh - 24px);
}

.safe-area-bottom {
  /* 固定边距作为后备方案 */
  padding-bottom: 20px;

  /* iOS 11.0-11.2 使用 constant() */
  padding-bottom: constant(safe-area-inset-bottom);

  /* iOS 11.2+ 使用 env() */
  padding-bottom: env(safe-area-inset-bottom);
}

/* 针对特定设备的媒体查询 */
@media screen and (min-height: 800px) {
  .safe-area-top {
    padding-top: 32px;
    padding-top: constant(safe-area-inset-top);
    padding-top: env(safe-area-inset-top);
  }

  .safe-area-bottom {
    padding-bottom: 24px;
    padding-bottom: constant(safe-area-inset-bottom);
    padding-bottom: env(safe-area-inset-bottom);
  }
}

@media screen and (max-width: 380px) {
  .safe-area-top {
    padding-top: 20px;
    padding-top: constant(safe-area-inset-top);
    padding-top: env(safe-area-inset-top);
  }

  .safe-area-bottom {
    padding-bottom: 16px;
    padding-bottom: constant(safe-area-inset-bottom);
    padding-bottom: env(safe-area-inset-bottom);
  }
}
</style>

