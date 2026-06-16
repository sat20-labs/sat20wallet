<template>
  <input
    type="checkbox"
    :checked="checked"
    :disabled="disabled"
    @change="$emit('update:checked', ($event.target as HTMLInputElement).checked)"
    class="h-4 w-4 rounded border-gray-300 disabled:cursor-not-allowed disabled:opacity-60"
  />
</template>

<script setup lang="ts">
defineProps<{
  checked?: boolean
  disabled?: boolean
}>()

defineEmits<{
  (e: 'update:checked', value: boolean): void
}>()
</script>
