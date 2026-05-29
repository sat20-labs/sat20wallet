<template>
  <Card class="p-1 sm:p-1">
    <div class="flex items-center px-1 py-0 text-xs overflow-hidden">
      <div class="flex-1 overflow-hidden truncate">
        <slot>
          {{ text }}
        </slot>
      </div>
      <Separator orientation="vertical" class="h-full mx-2" />
      <CopyButton :text="text" />
    </div>
  </Card>
</template>

<script setup lang="ts">
import { Card } from '@/components/ui/card'
import { Separator } from '@/components/ui/separator'
import CopyButton from '@/components/common/CopyButton.vue'
interface Props {
  text?: string | null
}
defineProps<Props>()
</script>
