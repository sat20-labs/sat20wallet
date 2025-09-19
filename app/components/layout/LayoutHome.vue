<template>
  <div class="layout-container h-full flex flex-col w-full">
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

