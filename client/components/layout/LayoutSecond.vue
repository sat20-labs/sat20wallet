<template>
  <div class="h-full flex flex-col w-full">
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
