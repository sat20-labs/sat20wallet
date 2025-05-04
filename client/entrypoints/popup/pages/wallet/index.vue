<template>
  <LayoutHome class="">
    <WalletHeader />
    <!-- 钱包地址 -->
    <div class="flex items-center justify-between p-2 rounded-lg bg-muted/80 hover:bg-muted transition-all">
      <!-- 圆形背景 + 居中 Icon -->
      <span
        class="w-9 h-9 flex items-center justify-center bg-gradient-to-tr from-[#6600cc] to-[#a0076d] text-foreground rounded-full">
        <Icon icon="lucide:user-round" class="w-5 h-5 text-white/80 flex-shrink-0" />
      </span>

      <!-- 账户地址 -->
      <Button asChild variant="link" class="flex-1 text-center">
        <a :href="mempoolUrl" target="_blank" title="View Trade History">
          {{ hideAddress(showAddress) }}
        </a>
      </Button>

      <!-- 竖线分隔符 -->
      <Separator orientation="vertical" class="h-full mx-2" />

      <!-- 复制按钮 -->
      <CopyButton :text="address" class="text-foreground/50" />

      <!-- 下拉选择 -->
      <SubWalletSelector @wallet-changed="handleSubWalletChange" @wallet-created="handleSubWalletCreated" />
    </div>

    <!-- 资产余额 -->
    <BalanceSummary :key="selectedChainLabel" 
      :selectedTranscendingMode="transcendingModeStore.selectedTranscendingMode || 'poolswap'"
      :selectedChain="selectedChainLabel" :mempool-url="mempoolUrl"/>

    <!-- 资产列表 -->
    <AssetList class="mt-4" v-model:model-value="selectTab" @update:model-value="tabChange">
      <template #poolswap-content>
        <Tabs defaultValue="l1" class="w-full">
          <TabsList class="grid w-full grid-cols-3">
            <TabsTrigger v-for="item in items" :key="item.value" :value="item.value">
              {{ item.label }}
            </TabsTrigger>
          </TabsList>

          <TabsContent v-for="item in items" :key="item.value" :value="item.value">
            <div v-if="item.value === 'l1'">
              <L1Card v-model:selectedType="selectedType" :assets="l1Assets" mode="poolswap"
                @splicing_in="handleSplicingIn" @send="handleSend" @deposit="handleDeposit" />
            </div>

            <div v-else-if="item.value === 'channel'">
              <ChannelCard v-model:selectedType="selectedType" @splicing_out="handleSplicingOut"
                @unlock="handleUnlock" />
            </div>

            <div v-else-if="item.value === 'l2'">
              <L2Card v-model:selectedType="selectedType" :mode="'poolswap'" @lock="handleLock" @send="handleSend"
                @withdraw="handleWithdraw" />
            </div>
          </TabsContent>
        </Tabs>
      </template>
    </AssetList>

  </LayoutHome>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { storeToRefs } from 'pinia'
import { Icon } from '@iconify/vue'
import { hideAddress } from '~/utils'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Button } from '@/components/ui/button'
import LayoutHome from '@/components/layout/LayoutHome.vue'
import WalletHeader from '@/components/wallet/HomeHeader.vue'
import L1Card from '@/components/wallet/L1Card.vue'
import L2Card from '@/components/wallet/L2Card.vue'
import ChannelCard from '@/components/wallet/ChannelCard.vue'

import SubWalletSelector from '@/components/wallet/SubWalletSelector.vue'
import CopyButton from '@/components/common/CopyButton.vue'
import { useWalletStore, useL1Store, useChannelStore } from '@/store'
import { useL1Assets, useL2Assets } from '@/composables'
import { useAssetOperations } from '@/composables/useAssetOperations'
import { useRouter, useRoute } from 'vue-router'
import { useToast } from '@/components/ui/toast'
import satsnetStp from '@/utils/stp'
import AssetList from '@/components/wallet/AssetList.vue'
import BalanceSummary from '@/components/asset/BalanceSummary.vue'
import { useTranscendingModeStore } from '@/store'
import { Chain } from '@/types/index'
import { useGlobalStore } from '@/store/global'


console.log('Debug: This is index.vue')

// 钱包数据
const walletStore = useWalletStore()
const l1Store = useL1Store()
const transcendingModeStore = useTranscendingModeStore()

const { refreshL1Assets } = useL1Assets()
const { refreshL2Assets } = useL2Assets()

let { address, network } = storeToRefs(walletStore)

const channelStore = useChannelStore()
const { channel} = storeToRefs(channelStore)
const { plainList, sat20List, brc20List, runesList } = storeToRefs(l1Store)

// 状态管理
const selectTab = ref('l1')
//const selectedType = ref('BTC')
const selectedType = ref('ORDX')

const globalStore = useGlobalStore()
const { env } = storeToRefs(globalStore)

const showAddress = computed(() => {
  if (selectedChainLabel.value === 'bitcoin') {
    return address.value
  } else if (selectedChainLabel.value === 'channel') {
    return channel.value?.channelId || address.value // 显示通道ID(address)
  } else if (selectedChainLabel.value === 'satoshinet') {
    return address.value
  }
  return '' // 默认返回空字符串
})

const mempoolUrl = computed(() => {
  if (selectedChainLabel.value === 'bitcoin') {
    return generateMempoolUrl({
      network: 'testnet',
      path: `address/${address.value}`,
    })
  } else if (selectedChainLabel.value === 'channel') {    
    return generateMempoolUrl({
      network: 'testnet',
      path: `address/${channel.value?.channelId || address.value}`,
      chain: Chain.SATNET,
      env: env.value,
    })
  } else if (selectedChainLabel.value === 'satoshinet') {
    return generateMempoolUrl({
      network: 'testnet',
      path: `address/${address.value}`,
      chain: Chain.SATNET,
      env: env.value,
    })
  }
  return '' // 默认返回空字符串，防止未匹配的情况
})


const l1Assets = computed(() => {
  switch (selectedType.value) {
    case 'BTC':
      return plainList.value || []
    case 'ORDX':
      return sat20List.value || []
    case 'Runes':
      return runesList.value || []
    case 'BRC20':
      return brc20List.value || []
    default:
      return []
  }
})

// 导航项
const items = [
  {
    label: 'Bitcoin',
    value: 'l1',
  },
  {
    label: 'Channel',
    value: 'channel',
  },
  {
    label: 'SatoshiNet',
    value: 'l2',
  },
]

const selectedChainLabel = computed(() => {
  //console.log('父组件 selectTab:', selectTab.value)
  const selectedItem = items.find(item => item.value === selectTab.value)
  //console.log('父组件 selectedItem:', selectedItem)
  return selectedItem ? selectedItem.label.toLowerCase() : 'unknown' // 默认值为 'unknown'
})

// 路由和工具
const router = useRouter()
const route = useRoute()
const { toast } = useToast()

// 资产操作
const {
  handleSend,
  handleDeposit,
  handleWithdraw,
  handleLock,
  handleUnlock,
  handleSplicingIn,
  handleSplicingOut,
} = useAssetOperations()

// 处理钱包切换
const handleSubWalletChange = async (wallet: any) => {
  console.log('SubWallet changed:', wallet)
  // 重新加载资产列表
  await refreshL1Assets()
  await refreshL2Assets()
}

// 处理新钱包创建
const handleSubWalletCreated = async (wallet: any) => {
  console.log('New SubWallet created:', wallet)
  // 重新加载资产列表
  await refreshL1Assets()
  await refreshL2Assets()
}

// 处理通道回调
const channelCallback = async (e: any) => {
  console.log('channel callback')
  let msg = ''
  const channelHandler = async () => {
    await channelStore.getAllChannels()
  }
  switch (e) {
    case 'splicingin':
      msg = 'splicing in success'
      await channelHandler()
      await refreshL1Assets()
      break
    case 'expanded"':
      msg = 'splicing in success'
      await channelHandler()
      await refreshL1Assets()
      break

    case 'splicingout':
      msg = 'splicing out success'
      await channelHandler()
      await refreshL1Assets()
      break
    case 'channelopened':
      msg = 'channel opened'
      await channelHandler()
      await refreshL1Assets()
      break
    case 'channelclosed':
      msg = 'channel closed'
      await channelHandler()
      await refreshL1Assets()
      break
    case 'utxounlockedorlocked':
      msg = 'utxo change'
      await channelHandler()
      await refreshL2Assets()
      break
    default:
      refreshL1Assets()
      refreshL2Assets()
      channelHandler()
      break
  }
  if (msg) {
    toast({
      title: 'success',
      description: msg,
    })
  }
}

// 监听路由变化
const handleRouteChange = () => {
  if (route.query?.tab) {
    selectTab.value = route.query.tab as string
  }
}

console.log('Debug2: This is index.vue')

const tabMapping = {
  bitcoin: 'l1',
  channel: 'channel',
  satoshinet: 'l2',
}

const tabChange = (i: string) => {
  const tabValue = tabMapping[i.toLowerCase() as keyof typeof tabMapping] || i
  const isValidTab = items.some(item => item.value === tabValue)
  if (!isValidTab) {
    console.error('Invalid tab value:', tabValue)
    return
  }

  selectTab.value = tabValue
  console.log('父组件 tabChange:', tabValue)
  router.replace({
    path: '/wallet',
    query: {
      tab: i,
    },
  })
}



// 生命周期钩子
onMounted(async () => {
  handleRouteChange()
  satsnetStp.registerCallback(channelCallback)
})
</script>
