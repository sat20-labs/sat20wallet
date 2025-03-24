<!-- <template>
  <div class="space-y-4">
    <L1AssetsTabs
      :model-value="selectedType"
      :assets="assets"
      @update:model-value="$emit('update:selectedType', $event)"
      @splicing_in="$emit('splicing_in', $event)"
      @send="$emit('send', $event)"
    />
  </div>
</template> -->

<template>
  <div class="space-y-4">   
    <L1AssetsTabs
      :model-value="selectedType"
      :assets="assets"
      @update:model-value="$emit('update:selectedType', $event)"
      @splicing_in="$emit('splicing_in', $event)"
      @send="$emit('send', $event)"
    />
  </div>
</template>

<script setup lang="ts">
import { PropType } from 'vue'
import L1AssetsTabs from '@/components/asset/L1AssetsTabs.vue'

interface Asset {
  id: string
  ticker: string
  label: string
  amount: number
  type?: string
}

defineProps({
  selectedType: {
    type: String,
    required: true
  },
  assets: {
    type: Array as PropType<Asset[]>,
    required: true,
    default: () => []
  }
})

defineEmits(['update:selectedType', 'splicing_in', 'send'])
</script>
