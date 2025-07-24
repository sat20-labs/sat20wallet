<template>
  <div class="space-y-4">
    <!-- Asset Type Tabs -->
    <div class="flex justify-between border-b border-zinc-700 mb-4">
      <nav class="flex -mb-px gap-4">
        <button v-for="(type, index) in assetTypes" :key="index" @click="selectedType = type"
          class="pb-2 px-1 font-mono font-semibold text-sm relative" :class="{
            'text-foreground/90': selectedType === type,
            'text-muted-foreground': selectedType !== type
          }">
          {{ $t(`channelAssetsTabs.assetType.${type}`) }}
          <div class="absolute bottom-0 left-0 right-0 h-0.5 transition-all" :class="{
            'bg-gradient-to-r from-primary to-primary/50 scale-x-100': selectedType === type,
            'scale-x-0': selectedType !== type
          }" />
        </button>
      </nav>

      <div class="flex items-center p-1 mb-1 gap-2">
        <span variant="link"
          class="flex justify-center p-1 h-6 mb-1 border border-zinc-600 hover:bg-zinc-700 rounded-sm">
          <a :href="`https://mempool.space/testnet4/address/${channel?.address || ''}`" target="_blank"
            class="mb-[1px]] hover:text-primary" :title="$t('channelAssetsTabs.viewTradeHistory')">
            <Icon icon="quill:link" class="w-4 h-4 text-zinc-400 hover:text-primary/90" />
          </a>
        </span>
        <span variant="link"
          class="flex justify-center items-center p-1 h-6 mb-1 border border-zinc-600 hover:bg-zinc-700 rounded-sm">
          <a :href="mempoolUrl" target="_blank" class="mb-[1px] hover:text-primary"
            :title="$t('channelAssetsTabs.viewL2History')">
            <span class="text-xs  p-[1px] text-zinc-400 hover:text-primary"> L2 </span>
          </a>
        </span>
      </div>
    </div>

    <!-- Asset Lists -->
    <div class="space-y-2">
      <div v-for="asset in filteredAssets" :key="asset.id"
        class="flex justify-left pl-1 pr-3 py-3 rounded-lg bg-muted border hover:border-primary/40 transition-colors">
        <!-- 圆形背景 + 居中 Icon -->
        <div
          class="w-12 h-10 mt-3 flex items-center justify-center rounded-full bg-zinc-700 text-zinc-200 font-bold text-lg">
          <!-- <img v-if="asset.logo" :src="asset.logo" alt="logo" class="w-full h-full object-cover rounded-full" /> -->
          <span class="flex justify-center items-center w-10 h-10">{{ asset.label.charAt(0).toUpperCase() }}</span>
        </div>

        <div class="flex flex-col justify-between w-full h-full ml-3">
          <!-- 第一行：资产名称和数量 -->
          <div class="flex justify-between items-center">
            <div class="font-medium text-zinc-400">{{ asset.label.toLocaleUpperCase() }}</div>
            <div class="text-sm font-semibold text-zinc-300">
              {{ formatAmount(asset) }}
            </div>
          </div>

          <!-- 第二行：操作按钮 -->
          <div class="flex justify-end gap-2 mt-2">
            <Button size="sm" variant="outline" class="border border-zinc-700/50 hover:bg-zinc-700 gap-[1px]"
              @click="$emit('splicing_out', asset)">
              <Icon icon="lets-icons:sign-out-squre" class="w-4 h-4 mr-1" />
              {{ $t('channelAssetsTabs.splicingOut') }}
            </Button>
            <Button size="sm" variant="outline" class="border border-zinc-700/50 hover:bg-zinc-700 gap-[1px]"
              @click="$emit('unlock', asset)">
              <Icon icon="lucide:unlock" class="w-4 h-4 mr-1" />
              {{ $t('channelAssetsTabs.unlock') }}
            </Button>
          </div>
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
import { useChannelStore, useWalletStore } from '~/store'
import { Chain } from '@/types/index'
import { useGlobalStore } from '@/store/global'
import { formatLargeNumber } from '@/utils'

const channelStore = useChannelStore()
const walletStore = useWalletStore()
const { network } = storeToRefs(walletStore)
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

const globalStore = useGlobalStore()
const { env } = storeToRefs(globalStore)

const mempoolUrl = computed(() => {

  return generateMempoolUrl({
    network: network.value,
    path: `address/${channel.value?.channelId || ''}`,
    chain: Chain.SATNET,
    env: env.value,
  })

  return '' // 默认返回空字符串，防止未匹配的情况
})

// 资产类型
//const assetTypes = ['BTC', 'ORDX', 'Runes', 'BRC20']
const assetTypes = ['ORDX', 'Runes', 'BRC20']
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
    return `${Number(asset.amount)} sats`
  }
  return `${formatLargeNumber(Number(asset.amount))}`
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
