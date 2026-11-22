<template>
  <div class="flex min-w-0 flex-col gap-4">
    <div class="min-w-0 break-words">
      <slot name="default" />
    </div>
    <div class="sticky bottom-0 z-10 shrink-0 border-t border-border bg-background py-3">
      <div>
        <slot name="footer">
          <div class="grid grid-cols-2 gap-2 sm:gap-3">
            <Button variant="outline" @click="$emit('cancel')" :disabled="loading" class="min-h-11 touch-manipulation">
              {{ $t('common.cancel', 'Cancel') }}
            </Button>
            <Button @click="$emit('confirm')" :disabled="loading || confirmDisabled" class="min-h-11 touch-manipulation">
              {{ $t('common.confirm', 'Confirm') }}
            </Button>
          </div>
        </slot>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { Button } from '@/components/ui/button'

interface Props {
  loading?: boolean
  confirmDisabled?: boolean
}

defineProps<Props>()
</script>
