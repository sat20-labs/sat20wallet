<template>
  <div class="h-full flex flex-col gap-4">
    <div class="flex-1 overflow-hidden">
      <ScrollArea class="h-full">
        <slot name="default" />
      </ScrollArea>
    </div>
    <div class="h-14 px-4">
      <div class="h-full">
        <slot name="footer">
          <div class="h-full grid grid-cols-2 gap-4">
            <Button variant="outline" @click="$emit('cancel')">Cancel</Button>
            <Button @click="$emit('confirm')">Confirm</Button>
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
