<template>
  <div class="layout-container flex h-full min-h-0 w-full flex-col pt-[env(safe-area-inset-top)]">
    <div class="min-h-0 flex-1 overflow-hidden">
      <ScrollArea class="h-full">
        <div class="mx-auto max-w-5xl px-4 py-4 sm:px-6">
          <slot />
        </div>
      </ScrollArea>
    </div>
    <NavFooter />
  </div>
</template>

<script setup lang="ts">
import { ScrollArea } from '@/components/ui/scroll-area'
import NavFooter from '@/components/layout/NavFooter.vue'
</script>
