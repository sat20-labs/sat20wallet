<template>
  <div class="h-full flex flex-col w-full safe-area-top">
    <div class="flex items-center px-4 py-3 border-b">
      <Button variant="ghost" size="icon" @click="router.back()">
        <Icon icon="solar:alt-arrow-left-line-duotone" />
      </Button>
      <h1 class="ml-4 text-lg font-medium">{{ title }}</h1>
    </div>
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
import { Button } from '@/components/ui/button'
import NavFooter from '@/components/layout/NavFooter.vue'
import { useRouter } from 'vue-router'
import { Icon } from '@iconify/vue'
const router = useRouter()

// Define props
defineProps<{
  title?: string
}>()
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

/* 针对特定设备的媒体查询 */
@media screen and (min-height: 800px) {
  .safe-area-top {
    padding-top: 32px;
    padding-top: constant(safe-area-inset-top);
    padding-top: env(safe-area-inset-top);
  }
}

@media screen and (max-width: 380px) {
  .safe-area-top {
    padding-top: 20px;
    padding-top: constant(safe-area-inset-top);
    padding-top: env(safe-area-inset-top);
  }
}
</style>