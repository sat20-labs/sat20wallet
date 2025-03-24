<template>
  <LayoutHome class="">
    <WalletHeader />

    <!-- 钱包地址 -->
    <CopyCard :text="address" class="w-full mb-4 bg-black/15">
      <Button asChild variant="link">
        <a
          :href="`https://mempool.space/zh/testnet4/address/${address}`"
          target="_blank"
        >
          {{ hideAddress(address) }}
        </a>
      </Button>
    </CopyCard>
    <div class="mb-4">12312312</div>
    <!-- 池子充值、提取模式 -->
    <TranscendingMode class="mb-4">
      <template #poolswap-content>
        <Tabs
          defaultValue="l1"
          v-model:model-value="selectTab"
          @update:model-value="tabChange"
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
                v-model:selectedType="selectedType"
                @splicing_in="handleSplicingIn"
                @send="handleSend"
                @deposit="handleDeposit"
                @withdraw="handleWithdraw"
              />
            </div>
            <div v-else-if="item.value === 'channel'">
              <ChannelCard 
                v-model:selectedType="selectedType"
                @lock="handleLock"
                @unlock="handleUnlock"
              />
            </div>
            <div v-else-if="item.value === 'l2'">
              <L2Card 
                v-model:selectedType="selectedType"
                :address="address || undefined"
                @deposit="handleDeposit"
                @withdraw="handleWithdraw"
              />
            </div>
          </TabsContent>
        </Tabs>
      </template>
    </TranscendingMode>
  </LayoutHome>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { storeToRefs } from 'pinia'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Button } from '@/components/ui/button'
import LayoutHome from '@/components/layout/LayoutHome.vue'
import WalletHeader from '@/components/wallet/HomeHeader.vue'
import AccountCard from '@/components/wallet/AccountCard.vue'
import ActionButtons from '@/components/wallet/ActionButtons.vue'
import ChainSwitcher from '@/components/wallet/ChainSwitcher.vue'
import L1Card from '@/components/wallet/L1Card.vue'
import L2Card from '@/components/wallet/L2Card.vue'
import ChannelCard from '@/components/wallet/ChannelCard.vue'
import TranscendingMode from '@/components/wallet/TranscendingMode.vue'
import CopyCard from '@/components/common/CopyCard.vue'
import { useWalletStore, useChannelStore } from '@/store'
import { useRouter, useRoute } from 'vue-router'
import satsnetStp from '@/utils/stp'
import { useL1Assets, useL2Assets } from '@/composables'
import { generateMempoolUrl, satsToBtc, hideAddress } from '@/utils'
import { useToast } from '@/components/ui/toast'

// 钱包数据
const walletStore = useWalletStore()
const { address } = storeToRefs(walletStore)

const { refreshL1Assets } = useL1Assets()
const { refreshL2Assets } = useL2Assets()

const router = useRouter()
const route = useRoute()
const { toast } = useToast()
const channelStore = useChannelStore()
const { network } = storeToRefs(walletStore)

const selectTab = ref<any>('l1')
const selectedType = ref('BTC') // 添加 selectedType

const items = [
  {
    label: 'BTC',
    value: 'l1',
  },
  {
    label: 'Lightning',
    value: 'channel',
  },
  {
    label: 'SatoshiNet',
    value: 'l2',
  },
]

// 处理splicing in
const handleSplicingIn = (asset: any) => {
  console.log('Splicing in:', asset)
  router.push(`/wallet/asset?type=splicing_in&p=l1&t=${asset.type}&a=${asset.id}`)
}

// 处理send
const handleSend = (asset: any) => {
  console.log('Send:', asset)
  router.push(`/wallet/asset?type=send&p=l1&t=${asset.type}&a=${asset.id}`)
}

// 处理存款
const handleDeposit = (asset: any) => {
  console.log('Deposit:', asset)
  router.push(`/wallet/asset?type=deposit&p=l1&t=${asset.type}&a=${asset.id}`)
}

// 处理提款
const handleWithdraw = (asset: any) => {
  console.log('Withdraw:', asset)
  router.push(`/wallet/asset?type=withdraw&p=l1&t=${asset.type}&a=${asset.id}`)
}

// 处理锁定
const handleLock = (asset: any) => {
  console.log('Lock:', asset)
  router.push(`/wallet/asset?type=lock&p=l1&t=${asset.type}&a=${asset.id}`)
}

// 处理解锁
const handleUnlock = (asset: any) => {
  console.log('Unlock:', asset)
  router.push(`/wallet/asset?type=unlock&p=l1&t=${asset.type}&a=${asset.id}`)
}

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
      refreshL1Assets()
      break
    case 'expanded"':
      msg = 'splicing in success'
      await channelHandler()
      refreshL1Assets()
      break

    case 'splicingout':
      msg = 'splicing out success'
      await channelHandler()
      refreshL1Assets()
      break
    case 'channelopened':
      msg = 'channel opened'
      await channelHandler()
      refreshL1Assets()
      break
    case 'channelclosed':
      msg = 'channel closed'
      await channelHandler()
      refreshL1Assets()
      break
    case 'utxounlockedorlocked':
      msg = 'utxo change'
      await channelHandler()
      refreshL2Assets()
      break
    default:
      break
  }
  if (msg) {
    toast({
      title: 'Success',
      description: msg,
    })
  }
}

const tabChange = (i: any) => {
  console.log('tabChange', i)
  router.replace({
    path: '/wallet',
    query: {
      tab: i,
    },
  })
}
onMounted(() => {
  if (route.query?.tab) {
    selectTab.value = route.query!.tab as any
  }
  satsnetStp.registerCallback(channelCallback)
})
</script>
