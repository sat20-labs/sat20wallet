<script setup lang="ts">
import { PropType, computed } from 'vue'
import { Button } from '@/components/ui/button'

// 类型定义
interface L2Asset {
  id: string
  ticker: string
  label: string
  amount: number
  status: 'locked' | 'unlocked'
  expiration?: number
}

// Props定义
const props = defineProps({
  selectedType: {
    type: String,
    required: true
  },
  address: {
    type: String,
    default: ''
  },
  assets: {
    type: Array as PropType<L2Asset[]>,
    required: true,
    default: () => []
  }
})

// 事件定义
const emit = defineEmits(['release', 'send'])

// 过滤逻辑
const filteredAssets = computed(() => {
  return props.assets.filter(asset => {
    // 根据资产状态和类型过滤
    const typeMatch = asset.ticker.toLowerCase() === props.selectedType.toLowerCase()
    const statusMatch = asset.status === 'unlocked'
    return typeMatch && statusMatch
  })
})

// 格式化显示
const formatDetails = (asset: L2Asset) => {
  return asset.ticker === 'BTC' ? 
    `${asset.amount} sats` : 
    `${asset.amount} $${asset.ticker}`
}
</script>

<template>
  <div class="space-y-4">
    <!-- <div v-if="address" class="text-sm text-muted-foreground mb-4">
      L2 Address: {{ address }}
    </div> -->
    
    <div 
      v-for="asset in filteredAssets"
      :key="asset.id"
      class="p-4 rounded-lg bg-muted/40 hover:bg-muted/60 transition-colors"
    >
      <div class="flex items-center justify-between">
        <div>
          <h3 class="font-medium">{{ (asset.ticker || asset.label).toUpperCase() }}</h3>
          <p class="text-sm text-muted-foreground">
            {{ formatDetails(asset) }}
          </p>
          <p v-if="asset.expiration" class="text-xs text-orange-500 mt-1">
            Expires in {{ Math.ceil((asset.expiration - Date.now())/86400000) }} days
          </p>
        </div>
        <div class="flex gap-2">
          <!-- <Button 
            size="sm"
            variant="outline"
            @click="$emit('deposit', asset)"
          >
            Add Funds
          </Button> -->
          <Button
            size="sm"
            variant="outline"
            :disabled="asset.status !== 'unlocked'"
            @click="$emit('release', asset)"
          >
            {{ asset.status === 'unlocked' ? 'Release' : 'Locked' }}
          </Button>
        </div>
      </div>
    </div>
    
    <!-- <div v-if="filteredAssets.length === 0" class="text-center text-muted-foreground text-sm">
      No available L2 assets matching "{{ selectedType }}"
    </div> -->
  </div>
</template>