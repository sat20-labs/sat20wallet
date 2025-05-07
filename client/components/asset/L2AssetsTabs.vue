<template>
  <div class="space-y-4">
    <!-- Asset Type Tabs -->
    <div class="flex justify-between border-b border-zinc-700  mb-4">
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
          {{ $t(`l2AssetsTabs.assetType.${type}`) }}
          <div
            class="absolute bottom-0 left-0 right-0 h-0.5 transition-all"
            :class="{
              'bg-gradient-to-r from-primary to-primary/50 scale-x-100': selectedType === type,
              'scale-x-0': selectedType !== type
            }"
          />
        </button>
      </nav>
      <div class="flex items-center">
        <Button size="icon" variant="ghost" @click="handlerRefresh">
          <Icon icon="lets-icons:refresh-2-light" class="text-zinc-300 mb-[1px]"/>
        </Button>
        <Button size="icon" variant="ghost" as-child>
          <a :href="mempoolUrl" target="_blank" class="mb-[1px] hover:text-primary" :title="$t('l2AssetsTabs.viewTradeHistory')">
            <Icon icon="quill:link" class="text-zinc-400 hover:text-primary/90" />
          </a>
        </Button>
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
          <div class="font-medium rune-name">{{ (asset.label).toUpperCase() }}</div>
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
              class="border border-zinc-700/50 hover:bg-zinc-700 gap-[1px]"
              @click="$emit('send', asset)"
            >
              <Icon icon="lucide:send" class="w-4 h-4 mr-1" />
              {{ $t('l2AssetsTabs.send') }}
            </Button>
            <Button
              size="sm"
              variant="outline"
              class="border border-zinc-700/50 hover:bg-zinc-700 gap-[1px]"
              @click="$emit('lock', asset)"
            >
              <Icon icon="lucide:lock" class="w-4 h-4 mr-1" />
              {{ $t('l2AssetsTabs.lock') }}
            </Button>
          </template>

          <!-- Poolswap 模式按钮 -->
          <template v-else>
            <Button
              size="sm"
              variant="outline"
              class="border border-zinc-700/50 hover:bg-zinc-700 gap-[1px]"
              @click="$emit('send', asset)"
            >
              <Icon icon="lucide:send" class="w-4 h-4 mr-1" />
              {{ $t('l2AssetsTabs.send') }}
            </Button>
            <Button
              size="sm"
              variant="outline"
              class="border border-zinc-700/50 hover:bg-zinc-700 gap-[1px]"
              @click="$emit('withdraw', asset)"
            >
              <Icon icon="lucide:arrow-up-right" class="w-4 h-4 mr-1" />
              {{ $t('l2AssetsTabs.withdraw') }}
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
import { useWalletStore } from '@/store'
import { Chain } from '@/types/index'
import { generateMempoolUrl } from '@/utils'
import { useGlobalStore } from '@/store/global'

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

const emit = defineEmits(['update:modelValue', 'lock', 'send', 'withdraw', 'refresh'])

const walletStore = useWalletStore()
const globalStore = useGlobalStore()
const { address } = storeToRefs(walletStore)
const { env } = storeToRefs(globalStore)
const mempoolUrl = computed(() => {
  return generateMempoolUrl({
    network: 'testnet',
    path: `address/${address.value}`,
    chain: Chain.SATNET,
    env: env.value
  })
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
  return `${asset.amount}`
}

// 监听资产类型变化
watch(selectedType, (newType) => {
  emit('update:modelValue', newType)
})

const handlerRefresh = () => {
  //console.log('L2AssetsTabs - Refresh')
  emit('refresh')
}
</script>

<style scoped>
.router-link-active {
  text-decoration: none;
}
</style>