<template>
  <input
    type="checkbox"
    :checked="checked"
    @change="$emit('update:checked', ($event.target as HTMLInputElement).checked)"
    class="h-4 w-4 rounded border-gray-300"
  />
</template>

<script setup lang="ts">
defineProps<{
  checked?: boolean
}>()

defineEmits<{
  (e: 'update:checked', value: boolean): void
}>()
</script>


