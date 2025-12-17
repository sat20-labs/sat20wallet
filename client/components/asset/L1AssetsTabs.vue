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
          {{ $t(`l1AssetsTabs.assetType.${type}`) }}
          <div class="absolute bottom-0 left-0 right-0 h-0.5 transition-all" :class="{
            'bg-gradient-to-r from-primary to-primary/50 scale-x-100': selectedType === type,
            'scale-x-0': selectedType !== type
          }" />
        </button>
      </nav>
      <div class="flex items-center">
        <Button size="icon" variant="ghost" @click="handlerRefresh">
          <Icon icon="lets-icons:refresh-2-light" class="text-zinc-300 mb-[1px]" />
        </Button>
        <Button size="icon" variant="ghost" as-child>
          <a :href="mempoolUrl" target="_blank" class="mb-[1px] hover:text-primary"
            :title="$t('l1AssetsTabs.viewTradeHistory')">
            <Icon icon="quill:link" class="text-zinc-400 hover:text-primary/90" />
          </a>
        </Button>
      </div>
    </div>

    <!-- Asset Lists -->
    <div class="space-y-2">
      <div v-for="asset in filteredAssets" :key="asset.id"
        class="flex justify-left pl-1 pr-3 py-3 rounded-lg bg-muted border hover:border-primary/40 transition-colors">
        <!-- 圆形背景 + 居中 Icon -->
        <div
          class="w-12 h-10 mt-3 flex items-center justify-center rounded-full bg-zinc-700 text-zinc-300 font-medium text-lg">
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
            <!-- Lightning 模式按钮 -->
            <template v-if="mode === 'lightning'">
              <Button size="sm" variant="outline" @click="handleSend(asset)"
                class="text-zinc-400 border border-zinc-700/50 hover:bg-zinc-700 gap-[1px]">
                <Icon icon="lucide:send" class="w-4 h-4 mr-1" />
                {{ $t('l1AssetsTabs.send') }}
              </Button>
              <Button size="sm" variant="outline" @click="handleSplicingIn(asset)"
                class="text-zinc-400 border border-zinc-700/50 hover:bg-zinc-700 gap-[1px]">
                <Icon icon="lets-icons:sign-in-squre" class="w-4 h-4 mr-1" />
                {{ $t('l1AssetsTabs.splicingIn') }}
              </Button>
            </template>
            <!-- Poolswap 模式按钮 -->
            <template v-else>
              <Button size="sm" variant="outline" @click="handleSend(asset)"
                class="text-zinc-400 border border-zinc-700/50 hover:bg-zinc-700 gap-[1px]">
                <Icon icon="lucide:send" class="w-4 h-4 mr-1" />
                {{ $t('l1AssetsTabs.send') }}
              </Button>
              <Button size="sm" variant="outline" @click="handleDeposit(asset)"
                :disabled="!canDeposit"
                class="text-zinc-400 border border-zinc-700/50 hover:bg-zinc-700 gap-[1px]">
                <Icon icon="lucide:arrow-down-right" class="w-4 h-4 mr-1" />
                {{ $t('l1AssetsTabs.deposit') }}
              </Button>
            </template>
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
import { useWalletStore } from '@/store'
import { Chain, WalletType } from '@/types/index'
import { generateMempoolUrl, formatLargeNumber } from '@/utils'
import { useGlobalStore } from '@/store/global'
import { storeToRefs } from 'pinia'

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

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
  (e: 'splicing_in', asset: any): void
  (e: 'send', asset: any): void
  (e: 'deposit', asset: any): void
  (e: 'refresh'): void
}>()

const walletStore = useWalletStore()
const globalStore = useGlobalStore()
const { address, network, currentWalletType } = storeToRefs(walletStore)
const { env } = storeToRefs(globalStore)

// 只有助记词钱包才能使用 deposit
const canDeposit = computed(() => currentWalletType.value === WalletType.MNEMONIC)

const mempoolUrl = computed(() => {
  return generateMempoolUrl({
    network: network.value,
    path: `address/${address.value}`,
  })
})

// 资产类型
//const assetTypes = ['BTC', 'ORDX', 'Runes', 'BRC20']
const assetTypes = ['ORDX', 'Runes', 'BRC20']
const selectedType = ref(props.modelValue || assetTypes[0])

// 过滤资产
const filteredAssets = computed(() => {
  // console.log('L1AssetsTabs - Received Assets:', props.assets)
  // console.log('L1AssetsTabs - Selected Type:', selectedType.value)

  return props.assets.filter(asset => {
    if (!asset) return false
    return true
    // if (selectedType.value === 'BTC' && !asset.type) {
    //   console.log('L1AssetsTabs - Found BTC asset:', asset)
    //   return true
    // }
    // const assetType = asset.type?.toUpperCase()
    // console.log('L1AssetsTabs - Asset:', asset, 'Type:', assetType, 'Selected:', selectedType.value)
    // return selectedType.value === assetType
  })
})

// 事件处理函数
const handleSend = (asset: any) => {
  // console.log('L1AssetsTabs - Send:', asset)
  emit('send', asset)
}

const handleSplicingIn = (asset: any) => {
  // console.log('L1AssetsTabs - Splicing In:', asset)
  emit('splicing_in', asset)
}

const handleDeposit = (asset: any) => {
  // console.log('L1AssetsTabs - Deposit:', asset)
  emit('deposit', asset)
}

// 监听资产类型变化
watch(selectedType, (newType) => {
  // console.log('L1AssetsTabs - Selected Type Changed:', newType)
  emit('update:modelValue', newType)
})

// 格式化金额显示
const formatAmount = (asset: Asset) => {
  if (selectedType.value === 'BTC') {
    return `${Number(asset.amount)} sats`
  }
  return `${formatLargeNumber(Number(asset.amount))}`
}

const handlerRefresh = () => {
  console.log('L1AssetsTabs - Refresh')
  emit('refresh')
}
</script>

<style scoped>
.router-link-active {
  text-decoration: none;
}
</style>