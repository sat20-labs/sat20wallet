<template>
  <div>
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
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { storeToRefs } from 'pinia'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import L1Card from '@/components/wallet/L1Card.vue'
import L2Card from '@/components/wallet/L2Card.vue'
import ChannelCard from '@/components/wallet/ChannelCard.vue'
import { useWalletStore } from '@/store'
import { useRouter } from 'vue-router'

// 钱包数据
const walletStore = useWalletStore()
const { address } = storeToRefs(walletStore)

const router = useRouter()
const selectTab = ref('l1')
const selectedType = ref('Bitcoin')

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

// 处理存款
const handleDeposit = (asset: any) => {
  console.log('Deposit:', asset)
}

// 处理提款
const handleWithdraw = (asset: any) => {
  console.log('Withdraw:', asset)
}

// 处理锁定
const handleLock = (asset: any) => {
  console.log('Lock:', asset)
}

// 处理解锁
const handleUnlock = (asset: any) => {
  console.log('Unlock:', asset)
}

const tabChange = (i: any) => {
  router.replace({
    query: {
      tab: i,
    },
  })
}
</script>
