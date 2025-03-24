<script setup lang="ts">
import { PropType, computed, onMounted } from 'vue'
import { Button } from '@/components/ui/button'

// 类型定义
interface Asset {
  id: string
  ticker: string
  label: string
  amount: number
}

// Props定义
const props = defineProps({
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

// 事件定义
const emit = defineEmits(['Splicing in',  'Send', 'update:selectedType'])

// 格式化金额显示
const formatAmount = (asset: Asset) => {
  if (asset.ticker === 'BTC') {
    return `${asset.amount} sats`
  }
  return `${asset.amount} $${asset.ticker}`
}

// 组件挂载调试
onMounted(() => {
  console.log('L1Card initialized with:', {
    assets: props.assets,
    selectedType: props.selectedType
  })
})
</script>

<template>
  <div class="space-y-2">
    <div
      v-for="asset in assets"
      :key="asset.id"
      class="flex items-center justify-between p-3 rounded-lg bg-background"
    >
      <div>
        <div class="font-medium">{{ asset.label || asset.ticker }}</div>
        <div class="text-sm text-muted-foreground">
          {{ formatAmount(asset) }}
        </div>
      </div>
      <div v-if="selectedType === 'Bitcoin'" class="flex gap-2">
        <Button 
          size="sm"
          variant="outline"
          @click="$emit('Splicing in', asset)"
        >
          Splicing in
        </Button>
        <Button
          size="sm"
          variant="outline"
          @click="$emit('Send', asset)"
        >
          Send
        </Button>
      </div>
      <div v-else class="flex gap-2">
        <Button 
          size="sm"
          variant="outline"
          @click="$emit('Splicing in', asset)"
        >
          Splicing in
        </Button>        
      </div>
    </div>
  </div>
</template>