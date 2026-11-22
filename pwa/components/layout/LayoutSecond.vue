<template>
  <div class="flex h-full min-h-0 w-full flex-col pt-[env(safe-area-inset-top)]">
    <header class="shrink-0 border-b border-border bg-background">
      <div class="mx-auto flex min-h-14 max-w-3xl items-center gap-3 px-4 py-2">
        <Button variant="ghost" size="icon" class="size-11 shrink-0" :aria-label="$t('common.back')" @click="router.back()">
          <ChevronLeft aria-hidden="true" class="size-5" />
        </Button>
        <h1 class="min-w-0 break-words text-lg font-semibold leading-snug">{{ title }}</h1>
      </div>
    </header>
    <div class="min-h-0 flex-1 overflow-hidden">
      <ScrollArea class="h-full">
        <div class="mx-auto max-w-3xl px-4 py-4 sm:px-6 sm:py-6">
          <slot />
        </div>
      </ScrollArea>
    </div>
    <NavFooter v-if="!hideNav" />
  </div>
</template>

<script setup lang="ts">
import { ScrollArea } from '@/components/ui/scroll-area'
import { Button } from '@/components/ui/button'
import NavFooter from '@/components/layout/NavFooter.vue'
import { useRouter } from 'vue-router'
import { ChevronLeft } from 'lucide-vue-next'
const router = useRouter()

defineProps<{
  title?: string
  hideNav?: boolean
}>()
</script>
