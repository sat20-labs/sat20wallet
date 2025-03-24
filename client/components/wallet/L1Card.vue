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
    required: true,
  },
  // selectedAssetType: {
  //   type: String,
  //   required: true
  // },
  assets: {
    type: Array as PropType<Asset[]>,
    required: true,
    default: () => [],
  },
})

// 事件定义
const emit = defineEmits([
  'splicing_in',
  'deposit',
  'withdraw',
  'send',
  'update:selectedType',
])

// 格式化金额显示
const formatAmount = (asset: any, selectedAssetType: any) => {
  if (selectedAssetType === 'BTC') {
    return `${asset.amount} sats`
  }
  console.log('selectedAssetType:', selectedAssetType)
  return `${asset.amount} $${asset.ticker || asset.label}`
}

// 组件挂载调试
onMounted(() => {
  console.log('L1Card initialized with:', {
    assets: props.assets,
    selectedType: props.selectedType,
    // selectedAssetType: props.selectedAssetType
  })
})
</script>

<template>
  <div class="space-y-2">
    <div
      v-for="asset in assets"
      :key="asset.id"
      class="flex items-center justify-between p-3 rounded-lg bg-muted/40 hover:bg-muted/60 transition-colors"
    >
      <div>
        <div class="font-medium">
          {{ (asset.ticker || asset.label).toUpperCase() }}
        </div>
        <div class="text-sm text-muted-foreground">
          {{ formatAmount(asset, selectedType) }}
        </div>
      </div>

      <div class="flex gap-0.5">
        <Button
          v-if="selectedType === 'BTC'"
          size="sm"
          variant="outline"
          @click="$emit('send', asset)"
        >
          <Icon icon="lucide:arrow-big-right" class="w-4 h-4" />Send
        </Button>
        <Button
          size="sm"
          variant="outline"
          @click="$emit('splicing_in', asset)"
        >
          <Icon icon="lucide:corner-down-right" class="w-4 h-4" /> Splicing in
        </Button>
      </div>
      <!-- <div v-else class="flex gap-2">
        <Button 
          size="sm"
          variant="outline"
          @click="$emit('Splicing in', asset)"
        >
          Splicing in
        </Button>
      </div> -->
    </div>
  </div>
</template>
