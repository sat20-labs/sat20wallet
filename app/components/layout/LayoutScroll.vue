<template>
  <ScrollArea class="layout-container p-4 w-full h-full">
    <slot />
  </ScrollArea>
</template>

<script setup lang='ts'>
import { ScrollArea } from '@/components/ui/scroll-area'
</script>
