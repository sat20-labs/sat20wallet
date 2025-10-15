<template>
  <div class="h-full flex flex-col gap-3 sm:gap-4">
    <div class="flex-1 overflow-hidden">
      <ScrollArea class="h-full">
        <slot name="default" />
      </ScrollArea>
    </div>
    <div class="h-14 sm:h-16 px-3 sm:px-4">
      <div class="h-full">
        <slot name="footer">
          <div class="h-full grid grid-cols-2 gap-2 sm:gap-4">
            <Button variant="outline" @click="$emit('cancel')" class="text-sm sm:text-base h-full min-h-[44px] touch-manipulation">
              Cancel
            </Button>
            <Button @click="$emit('confirm')" class="text-sm sm:text-base h-full min-h-[44px] touch-manipulation">
              Confirm
            </Button>
          </div>
        </slot>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { Button } from '@/components/ui/button'
import { ScrollArea } from '@/components/ui/scroll-area'
</script>
