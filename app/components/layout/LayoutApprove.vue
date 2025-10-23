<template>
  <div class="flex flex-col gap-3 sm:gap-4 h-full">
    <div class="flex-1">
      <slot name="default" />
    </div>
    <div class="h-14 sm:h-16 px-3 sm:px-4 flex-shrink-0">
      <div class="h-full">
        <slot name="footer">
          <div class="h-full grid grid-cols-2 gap-2 sm:gap-4">
            <Button variant="outline" @click="$emit('cancel')" :disabled="loading" class="text-sm sm:text-base h-full min-h-[44px] touch-manipulation">
              {{ $t('common.cancel', 'Cancel') }}
            </Button>
            <Button @click="$emit('confirm')" :disabled="loading" class="text-sm sm:text-base h-full min-h-[44px] touch-manipulation">
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
}

defineProps<Props>()
</script>
