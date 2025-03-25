<template>
  <div class="space-y-4">   
    <L1AssetsTabs
      :model-value="selectedType"
      :assets="assets"
      :mode="mode"
      @update:model-value="$emit('update:selectedType', $event)"
      @splicing_in="$emit('splicing_in', $event)"
      @send="$emit('send', $event)"
      @deposit="$emit('deposit', $event)"
    />
  </div>
</template>

<script setup lang="ts">
import { PropType, watch } from 'vue'
import L1AssetsTabs from '@/components/asset/L1AssetsTabs.vue'

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
    required: true
  }
})

// 监听资产变化
watch(() => props.assets, (newAssets) => {
  console.log('L1Card - Assets changed:', newAssets)
}, { deep: true })

// 监听选中类型变化
watch(() => props.selectedType, (newType) => {
  console.log('L1Card - Selected type changed:', newType)
})

defineEmits(['update:selectedType', 'splicing_in', 'send', 'deposit'])
</script>
