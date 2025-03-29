<template>
  <div>
    <Tabs
      defaultValue="l1"
      v-model:model-value="selectTab"      
      class="w-full"
    >
      <TabsList class="grid w-full grid-cols-3">
        <TabsTrigger
          v-for="item in items"
          :key="item.value"
          :value="item.value"
          class="text-xs"
        >
          {{ item.label }}
        </TabsTrigger>
      </TabsList>

      <TabsContent
        v-for="item in items"
        :key="item.value"
        :value="item.value"
        class="mt-2"
      >
        <div v-if="item.value === 'l1'">
          <L1Card 
            :selectedType="selectedType"
            :assets="l1Assets"
            :mode="'poolswap'"
            @update:selectedType="selectedType = $event"
            @splicing_in="handleSplicingIn"
            @send="handleSend"
            @deposit="handleDeposit"
          />
        </div>
        <div v-else-if="item.value === 'channel'">
          <ChannelCard 
            :selectedType="selectedType"
            :address="address"
            @update:selectedType="selectedType = $event"             
            @splicing_out="handleSplicingOut"
            @unlock="handleUnlock"
          />
        </div>
        <div v-if="item.value === 'pool'"><PoolManager /></div>
        <div v-else-if="item.value === 'l2'">
          <L2Card 
            :selectedType="selectedType"
            :assets="l2Assets"
            :mode="'poolswap'"           
            @update:selectedType="selectedType = $event"
            @lock="handleLock"
            @send="handleSend"
            @withdraw="handleWithdraw"
          />
        </div>
      </TabsContent>
    </Tabs>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { storeToRefs } from 'pinia'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import L1Card from '@/components/wallet/L1Card.vue'
import L2Card from '@/components/wallet/L2Card.vue'
import ChannelCard from '@/components/wallet/ChannelCard.vue'
import { useWalletStore, useL1Store, useL2Store } from '@/store'
import { useRouter } from 'vue-router'

// 钱包数据
const walletStore = useWalletStore()
const l1Store = useL1Store()
const l2Store = useL2Store()

const { address } = storeToRefs(walletStore)

const router = useRouter()
const selectTab = ref('l1')
const selectedType = ref('BTC')

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

// 资产列表
const l1Assets = computed(() => {
  switch (selectedType.value) {
    case 'BTC':
      return (l1Store.plainList || []).map(asset => ({ ...asset, type: 'BTC' }))
    case 'SAT20':
      return (l1Store.sat20List || []).map(asset => ({ ...asset, type: 'SAT20' }))
    case 'Runes':
      return (l1Store.runesList || []).map(asset => ({ ...asset, type: 'Runes' }))
    case 'BRC20':
      return (l1Store.brc20List || []).map(asset => ({ ...asset, type: 'BRC20' }))
    default:
      return []
  }
})

const l2Assets = computed(() => {
  switch (selectedType.value) {
    case 'BTC':
      return (l2Store.plainList || []).map(asset => ({ ...asset, type: 'BTC' }))
    case 'SAT20':
      return (l2Store.sat20List || []).map(asset => ({ ...asset, type: 'SAT20' }))
    case 'Runes':
      return (l2Store.runesList || []).map(asset => ({ ...asset, type: 'Runes' }))
    case 'BRC20':
      return (l2Store.brc20List || []).map(asset => ({ ...asset, type: 'BRC20' }))
    default:
      return []
  }
})

// 处理存款
const handleDeposit = (asset: any) => {
  console.log('Deposit:', asset)
  router.push(
    `/wallet/asset?type=deposit&p=${asset.protocol || 'btc'}&t=${asset.type}&a=${asset.id}`
  )
}

// 处理提款
const handleWithdraw = (asset: any) => {
  console.log('Withdraw:', asset)
  router.push(
    `/wallet/asset?type=withdraw&p=${asset.protocol || 'btc'}&t=${asset.type}&a=${asset.id}`
  )
}

// 处理发送
const handleSend = (asset: any) => {
  console.log('Send:', asset)
  router.push(
    `/wallet/asset?type=${selectTab.value === 'l1' ? 'l1_send' : 'l2_send'}&p=${asset.protocol || 'btc'}&t=${asset.type}&a=${asset.id}`
  )
}

// 处理锁定
const handleLock = (asset: any) => {
  console.log('Lock:', asset)
  router.push(
    `/wallet/asset?type=lock&p=${asset.protocol || 'btc'}&t=${asset.type}&a=${asset.id}`
  )
}

// 处理解锁
const handleUnlock = (asset: any) => {
  console.log('Unlock:', asset)
  router.push(
    `/wallet/asset?type=unlock&p=${asset.protocol || 'btc'}&t=${asset.type}&a=${asset.id}`
  )
}

// 处理 Splicing in
const handleSplicingIn = (asset: any) => {
  console.log('Splicing in:', asset)
  router.push(
    `/wallet/asset?type=splicing_in&p=${asset.protocol || 'btc'}&t=${asset.type}&a=${asset.id}`
  )
}

// 处理 Splicing out
const handleSplicingOut = (asset: any) => {
  console.log('Splicing out:', asset)
  router.push(
    `/wallet/asset?type=splicing_out&p=${asset.protocol || 'btc'}&t=${asset.type}&a=${asset.id}`
  )
}

// 切换标签
const tabChange = (value: string) => {
  selectTab.value = value
  selectedType.value = 'BTC'
}
</script>
