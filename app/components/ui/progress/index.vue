<template>
  <div class="relative" :class="className">
    <div
      class="relative h-2 w-full overflow-hidden rounded-full bg-muted"
      role="progressbar"
      :aria-valuenow="value"
      aria-valuemin="0"
      aria-valuemax="100"
    >
      <div
        class="h-full w-full flex-1 bg-primary transition-all duration-300 ease-in-out"
        :style="{ transform: `translateX(-${100 - (value || 0)}%)` }"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
interface Props {
  value?: number
  className?: string
}

withDefaults(defineProps<Props>(), {
  value: 0,
  className: ''
})
</script> 
