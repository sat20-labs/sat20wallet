<template>
  <div class="space-y-4">
    <!-- Asset Type Tabs -->
    <div class="border-b border-border/50 mb-4">
      <nav class="flex -mb-px gap-4">
        <button
          v-for="(type, index) in assetTypes"
          :key="index"
          @click="selectedType = type"
          class="pb-2 px-1 font-mono font-semibold text-sm relative"
          :class="{
            'text-foreground/90': selectedType === type,
            'text-muted-foreground': selectedType !== type
          }"
        >
          {{ type }}
          <div
            class="absolute bottom-0 left-0 right-0 h-0.5 transition-all"
            :class="{
              'bg-gradient-to-r from-primary to-primary/50 scale-x-100': selectedType === type,
              'scale-x-0': selectedType !== type
            }"
          />
        </button>
      </nav>
    </div>

    <!-- Asset Lists -->
    <div class="space-y-2">
      <div
        v-for="asset in filteredAssets"
        :key="asset.id"
        class="flex items-center justify-between p-3 rounded-lg bg-muted border hover:border-primary/40 transition-colors"
      >
        <div>
          <div class="font-medium">{{ (asset.ticker || asset.label).toUpperCase() }}</div>
          <div class="text-sm text-muted-foreground">
            {{ formatAmount(asset) }}
          </div>
        </div>

        <div class="flex gap-2">
          <!-- Lightning 模式按钮 -->
          <template v-if="mode === 'lightning'">
            <Button
              size="sm"
              variant="outline"
              @click="$emit('send', asset)"
            >
              <Icon icon="lucide:arrow-right" class="w-4 h-4 mr-1" />
              Send
            </Button>
            <Button
              size="sm"
              variant="outline"
              @click="$emit('lock', asset)"
            >
              <Icon icon="lucide:corner-up-right" class="w-4 h-4 mr-1" />
              Lock
            </Button>
          </template>

          <!-- Poolswap 模式按钮 -->
          <template v-else>
            <Button
              size="sm"
              variant="outline"
              @click="$emit('send', asset)"
            >
              <Icon icon="lucide:arrow-right" class="w-4 h-4 mr-1" />
              Send
            </Button>
            <Button
              size="sm"
              variant="outline"
              @click="$emit('withdraw', asset)"
            >
              <Icon icon="lucide:arrow-up-right" class="w-4 h-4 mr-1" />
              Withdraw
            </Button>
          </template>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Icon } from '@iconify/vue'

// 类型定义
interface Asset {
  id: string
  ticker: string
  label: string
  amount: number
  type?: string
}

// Props定义
const props = defineProps<{
  modelValue?: string,
  assets: Asset[],
  mode: 'poolswap' | 'lightning'
}>()

const emit = defineEmits(['update:modelValue', 'lock', 'send',  'withdraw'])

// 资产类型
const assetTypes = ['BTC', 'SAT20', 'Runes', 'BRC20']
const selectedType = ref(props.modelValue || assetTypes[0])

// 过滤资产
const filteredAssets = computed(() => {
  return props.assets.filter(asset => {
    if (!asset) return false
    return true
    // if (selectedType.value === 'BTC' && !asset.type) return true
    // const assetType = asset.type?.toUpperCase()
    // return selectedType.value === assetType
  })
})

// 格式化金额显示
const formatAmount = (asset: Asset) => {
  if (selectedType.value === 'BTC') {
    return `${asset.amount} sats`
  }
  return `${asset.amount} ${asset.ticker}`
}

// 监听资产类型变化
watch(selectedType, (newType) => {
  emit('update:modelValue', newType)
})
</script>

<style scoped>
.router-link-active {
  text-decoration: none;
}
</style>