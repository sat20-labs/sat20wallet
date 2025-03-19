<template>
  <LayoutHome class="">
    <WalletHeader />

    <Tabs
      defaultValue="l1"
      v-model:model-value="selectTab"
      @update:model-value="tabChange"
      class="w-full mb-4"
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
          <L1Card />
        </div>
        <div v-else-if="item.value === 'channel'">
          <ChannelCard />
        </div>

        <div v-else-if="item.value === 'l2'">
          <L2Card />
        </div>
      </TabsContent>
    </Tabs>

    <!-- <ActionButtons
      @receive="handleReceive"
      @send="handleSend"
      @history="handleHistory"
      @buy="handleBuy"
    /> -->
  </LayoutHome>
</template>

<script setup lang="ts">
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import LayoutHome from '@/components/layout/LayoutHome.vue'
import WalletHeader from '@/components/wallet/HomeHeader.vue'
import AccountCard from '@/components/wallet/AccountCard.vue'
import ActionButtons from '@/components/wallet/ActionButtons.vue'
import ChainSwitcher from '@/components/wallet/ChainSwitcher.vue'
import L1Card from '@/components/wallet/L1Card.vue'
import L2Card from '@/components/wallet/L2Card.vue'
import ChannelCard from '@/components/wallet/ChannelCard.vue'
import { useWalletStore, useChannelStore } from '@/store'
import { useRouter, useRoute } from 'vue-router'
import satsnetStp from '@/utils/stp'
import { useL1Assets, useL2Assets } from '@/composables'
import { generateMempoolUrl, satsToBtc } from '@/utils'
import { useToast } from '@/components/ui/toast'
// 钱包数据

const { refreshL1Assets } = useL1Assets()
const { refreshL2Assets } = useL2Assets()

// const { balance } = storeToRefs(assetStore)
const router = useRouter()
const route = useRoute()
const { toast } = useToast()
const channelStore = useChannelStore()
const walletStore = useWalletStore()
const { address, network } = storeToRefs(walletStore)

const selectTab = ref<any>('l1')

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

const historyUrl = computed(() =>
  generateMempoolUrl({
    path: `/address/${address.value}`,
    network: network.value,
  })
)

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
