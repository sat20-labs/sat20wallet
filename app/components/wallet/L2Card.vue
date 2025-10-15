<template>
  <div class="space-y-4">   
    <L2AssetsTabs
      :model-value="selectedType"
      :assets="assets"
      :mode="mode"
      @update:model-value="$emit('update:selectedType', $event)"
      @lock="$emit('lock', $event)"
      @send="$emit('send', $event)"
      @refresh="$emit('refresh')"
      @withdraw="$emit('withdraw', $event)"
    />
  </div>
</template>

<script setup lang="ts">
import { PropType } from 'vue'
import L2AssetsTabs from '@/components/asset/L2AssetsTabs.vue'

interface Asset {
  id: string
  ticker: string
  label: string
  amount: number
  type?: string
}

type TranscendingMode = 'poolswap' | 'lightning'

const props = defineProps({
  selectedType: {
    type: String,
    required: true
  },
  assets: {
    type: Array as PropType<Asset[]>,
    required: true,
    default: () => []
  },
  mode: {
    type: String as PropType<TranscendingMode>,
    required: false,
    default: 'poolswap'
  },
  address: {
    type: String,
    required: false,
    default: null
  }
})

defineEmits(['update:selectedType', 'lock', 'send', 'withdraw', 'refresh'])
</script>