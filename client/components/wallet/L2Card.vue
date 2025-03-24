<template>
  <div class="space-y-4">
    <div v-if="address" class="text-sm text-muted-foreground mb-4">
      L2 Address: {{ address }}
    </div>
    
    <L2AssetsTabs
      :model-value="selectedType"
      @update:model-value="$emit('update:selectedType', $event)"
    />
  </div>
</template>

<script setup lang="ts">
import L2AssetsTabs from '@/components/asset/L2AssetsTabs.vue'

defineProps<{
  selectedType: string,
  address?: string | null
}>()

defineEmits(['update:selectedType'])
</script>