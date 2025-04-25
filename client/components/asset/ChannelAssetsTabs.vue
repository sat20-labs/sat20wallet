<template>
  <div class="space-y-4">
    <!-- Asset Type Tabs -->
    <div class="flex justify-between border-b border-zinc-700 mb-4">
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

      <div class="flex items-center p-1 mb-1 gap-2">
        <span variant="link" class="flex justify-center p-1 h-6 mb-1 border border-zinc-600 hover:bg-zinc-700 rounded-sm">        
          <a
            :href="`https://mempool.space/zh/testnet4/address/${channel.address}`"
            target="_blank" class="mb-[1px]] hover:text-primary" title="View Trade History"
          >
           <Icon icon="quill:link" class="w-4 h-4 text-zinc-400 hover:text-primary/90" />
          </a>
        </span>
        <span variant="link" class="flex justify-center items-center p-1 h-6 mb-1 border border-zinc-600 hover:bg-zinc-700 rounded-sm">
          <a
            :href="`https://mempool.dev.sat20.org/address/${channel.address}`"
            target="_blank" class="mb-[1px] hover:text-primary" title="View L2 History"
          >
          <span class="text-xs  p-[1px] text-zinc-400 hover:text-primary"> L2 </span> 
          </a>        
        </span>         
      </div>
    </div>

    <!-- Asset Lists -->
    <div class="space-y-2">
      <div
        v-for="asset in filteredAssets"
        :key="asset.id"
        class="flex items-center justify-between p-3 rounded-lg bg-muted border hover:border-primary/40 transition-colors"
      >
        <div>
          <div class="font-medium">{{ (asset.label).toUpperCase() }}</div>
          <div class="text-sm text-muted-foreground">
            {{ formatAmount(asset) }}
          </div>
        </div>

        <div class="flex gap-2">
          <Button
            v-if="selectedType === 'BTC'"
            size="sm"
            variant="outline"
            @click="$emit('splicing_out', asset)"
          >
            <Icon icon="lucide:corner-up-right" class="w-4 h-4 mr-1" />
            Splicing out
          </Button>
          <Button
            size="sm"
            variant="outline"
            @click="$emit('unlock', asset)"
          >
            <Icon icon="lucide:unlock" class="w-4 h-4 mr-1" />
            Unlock
          </Button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Icon } from '@iconify/vue'
import { storeToRefs } from 'pinia'
import { useChannelStore } from '~/store'

const channelStore = useChannelStore()
const { channel, plainList, sat20List, brc20List, runesList } = storeToRefs(channelStore)

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
  assets: Asset[]
}>()

const emit = defineEmits(['update:modelValue', 'splicing_out', 'unlock'])

// 资产类型
const assetTypes = ['BTC', 'ORDX', 'Runes', 'BRC20']
const selectedType = ref(props.modelValue || assetTypes[0])

// 过滤资产
const filteredAssets = computed(() => {
  return props.assets.filter(asset => {
    if (!asset) return false
    return true
  })
})

// 格式化金额显示
const formatAmount = (asset: Asset) => {
  if (selectedType.value === 'BTC') {
    return `${asset.amount} sats`
  }
  return `${asset.amount}`
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
