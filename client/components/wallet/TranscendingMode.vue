<template>
  <div class="space-y-4 w-full">
    <!-- Mode Selection -->
    <div class="mb-4 flex items-center gap-4">
      <Label class="text-sm text-foreground/50 shrink-0">Trans Mode:</Label>
      <div class="flex gap-2">
        <Button :variant="selectedTranscendingMode === 'poolswap' ? 'secondary' : 'outline'" 
          class="h-8 w-[100px] flex items-center justify-start px-3 rounded-md" @click="selectedTranscendingMode = 'poolswap'">
          <Icon icon="lucide:repeat" class="w-5 h-5 shrink-0" />
          <span class="text-xs">Poolswap</span>
        </Button>
        <Button :variant="selectedTranscendingMode === 'lightning' ? 'secondary' : 'outline'" 
          class="h-8 w-[100px] flex items-center justify-start px-3 rounded-md" @click="selectedTranscendingMode = 'lightning'">
          <Icon icon="lucide:zap" class="w-5 h-5 shrink-0" />
          <span class="text-xs">Lightning</span>
        </Button>
      </div>
    </div>

    <!-- Poolswap Mode -->
    <div v-if="selectedTranscendingMode === 'poolswap'">
      <Tabs defaultValue="bitcoin" v-model="selectedChain" class="w-full">
        <TabsList class="grid w-full grid-cols-3">
          <TabsTrigger value="bitcoin">
            <Icon icon="cryptocurrency:btc" class="w-4 h-4 mr-1 justify-self-center" />
            Bitcoin
          </TabsTrigger>
          <TabsTrigger value="pool">
            <Icon icon="lucide:waves" class="w-4 h-4 mr-1 justify-self-center" />
            Pool
          </TabsTrigger>
          <TabsTrigger value="satoshinet">
            <Icon icon="lucide:globe-lock" class="w-4 h-4 mr-1 justify-self-center" />
            SatoshiNet
          </TabsTrigger>
        </TabsList>

        <TabsContent value="bitcoin">
          <L1Card v-model:selectedType="selectedAssetType" :assets="filteredAssets" :mode="selectedTranscendingMode" 
            @splicing_in="handleSplicingIn" @send="handleSend" @deposit="handleDeposit" />
        </TabsContent>

        <TabsContent value="pool">
          <PoolManager />
        </TabsContent>

        <TabsContent value="satoshinet">
          <L2Card v-model:selectedType="selectedAssetType" :assets="filteredAssets" :mode="selectedTranscendingMode" 
            @lock="handleLock" @send="handleSend" @withdraw="handleWithdraw" />
        </TabsContent>
      </Tabs>
    </div>

    <!-- Lightning Mode -->
    <div v-else>
      <Tabs defaultValue="bitcoin" v-model="selectedChain" class="w-full">
        <TabsList class="grid w-full grid-cols-3">
          <TabsTrigger value="bitcoin">
            <Icon icon="cryptocurrency:btc" class="w-4 h-4 mr-1 justify-self-center" />
            Bitcoin
          </TabsTrigger>
          <TabsTrigger value="channel">
            <Icon icon="lucide:zap" class="w-4 h-4 mr-1 justify-self-center" />
            Channel
          </TabsTrigger>
          <TabsTrigger value="satoshinet">
            <Icon icon="lucide:globe-lock" class="w-4 h-4 mr-1 justify-self-center" />
            SatoshiNet
          </TabsTrigger>
        </TabsList>

        <TabsContent value="bitcoin">
          <L1Card v-model:selectedType="selectedAssetType" :assets="filteredAssets" :mode="selectedTranscendingMode" 
            @splicing_in="handleSplicingIn" @send="handleSend" @deposit="handleDeposit" />
        </TabsContent>

        <TabsContent value="channel">
          <ChannelCard v-model:selectedType="selectedAssetType" @splicing_out="handleSplicingOut" @unlock="handleUnlock" />
        </TabsContent>

        <TabsContent value="satoshinet">
          <L2Card v-model:selectedType="selectedAssetType" :assets="filteredAssets" :mode="selectedTranscendingMode" 
            @lock="handleLock" @send="handleSend" @withdraw="handleWithdraw" />
        </TabsContent>
      </Tabs>
    </div>

    <!-- Asset Operation Dialog -->
    <AssetOperationDialog 
      v-model:open="showDialog" 
      :title="operationTitle" 
      :description="operationDescription" 
      :amount="operationAmount"
      :asset-type="selectedAsset?.type"
      :asset-ticker="selectedAsset?.ticker"
      @update:amount="operationAmount = $event" 
      @confirm="handleOperationConfirm" 
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, nextTick, defineEmits } from 'vue'
import { useRouter } from 'vue-router'
import { Icon } from '@iconify/vue'
import { Label } from '@/components/ui/label'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import L1Card from '@/components/wallet/L1Card.vue'
import L2Card from '@/components/wallet/L2Card.vue'
import ChannelCard from '@/components/wallet/ChannelCard.vue'
import PoolManager from '@/components/wallet/PoolManager.vue'
import AssetOperationDialog from '@/components/wallet/AssetOperationDialog.vue'
import { useL1Store, useL2Store } from '@/store'
import { useChannelStore } from '@/store/channel'
import { useToast } from '@/components/ui/toast/use-toast'

const router = useRouter()
const channelStore = useChannelStore()
const { toast } = useToast()

// 状态管理
type TranscendingMode = 'poolswap' | 'lightning'
type ChainType = 'bitcoin' | 'satoshinet'

const selectedTranscendingMode = ref<TranscendingMode>('poolswap')
const selectedChain = ref<ChainType>('bitcoin')
const selectedAssetType = ref('BTC')

// Store
const l1Store = useL1Store()
const l2Store = useL2Store()

// 资产列表
const filteredAssets = computed(() => {
  let assets: any[] = []

  // 获取当前链的资产列表
  const getChainAssets = (isMainnet: boolean) => {
    const store = isMainnet ? l1Store : l2Store
    switch (selectedAssetType.value) {
      case 'BTC':
        return (store.plainList || []).map(asset => ({ ...asset, type: 'BTC' }))
      case 'SAT20':
        return (store.sat20List || []).map(asset => ({ ...asset, type: 'SAT20' }))
      case 'Runes':
        return (store.runesList || []).map(asset => ({ ...asset, type: 'Runes' }))
      case 'BRC20':
        return (store.brc20List || []).map(asset => ({ ...asset, type: 'BRC20' }))
      default:
        return []
    }
  }

  if (selectedTranscendingMode.value === 'lightning') {
    if (selectedChain.value === 'bitcoin') {
      assets = getChainAssets(true) // L1 资产
    } else if (selectedChain.value === 'satoshinet') {
      assets = getChainAssets(false) // L2 资产
    }
  } else {
    // Poolswap 模式
    const isMainnet = selectedChain.value === 'bitcoin'
    assets = getChainAssets(isMainnet)
  }

  console.log('Filtered Assets:', assets)
  return assets
})

// Dialog State
const showDialog = ref(false)
const operationAmount = ref('')
const operationType = ref('')
const selectedAsset = ref<any>(null)
const operationTitle = computed(() => {
  switch (operationType.value) {
    case 'send':
      return 'Send Asset'
    case 'deposit':
      return 'Deposit Asset'
    case 'withdraw':
      return 'Withdraw Asset'
    case 'lock':
      return 'Lock Asset'
    case 'unlock':
      return 'Unlock Asset'
    case 'splicing_in':
      return 'Splicing In'
    case 'splicing_out':
      return 'Splicing Out'
    default:
      return 'Asset Operation'
  }
})

const operationDescription = computed(() => {
  if (!selectedAsset.value) return ''
  const asset = selectedAsset.value
  const type = asset.type || 'BTC'
  const amount = asset.amount || 0
  return `${type}: ${amount} ${asset.ticker || 'sats'}`
})

// 事件处理
const emit = defineEmits<{
  (e: 'splicing_in', asset: any): void
  (e: 'send', asset: any): void
  (e: 'deposit', asset: any): void
  (e: 'withdraw', asset: any): void
  (e: 'lock', asset: any): void
  (e: 'unlock', asset: any): void
  (e: 'splicing_out', asset: any): void
}>()

const handleSplicingIn = (asset: any) => {
  console.log('TranscendingMode - Splicing In:', asset)
  operationType.value = 'splicing_in'
  selectedAsset.value = asset
  operationAmount.value = ''
  showDialog.value = true
}

const handleSend = (asset: any) => {
  console.log('TranscendingMode - Send:', asset)
  operationType.value = 'send'
  selectedAsset.value = asset
  operationAmount.value = ''
  showDialog.value = true
}

const handleDeposit = (asset: any) => {
  console.log('TranscendingMode - Deposit:', asset)
  operationType.value = 'deposit'
  selectedAsset.value = asset
  operationAmount.value = ''
  showDialog.value = true
}

const handleWithdraw = (asset: any) => {
  console.log('TranscendingMode - Withdraw:', asset)
  operationType.value = 'withdraw'
  selectedAsset.value = asset
  operationAmount.value = ''
  showDialog.value = true
}

const handleLock = (asset: any) => {
  console.log('TranscendingMode - Lock:', asset)
  operationType.value = 'lock'
  selectedAsset.value = asset
  operationAmount.value = ''
  showDialog.value = true
}

const handleUnlock = (asset: any) => {
  console.log('TranscendingMode - Unlock:', asset)
  operationType.value = 'unlock'
  selectedAsset.value = asset
  operationAmount.value = ''
  showDialog.value = true
}

const handleSplicingOut = (asset: any) => {
  console.log('TranscendingMode - Splicing Out:', asset)
  operationType.value = 'splicing_out'
  selectedAsset.value = asset
  operationAmount.value = ''
  showDialog.value = true
}

// 处理操作确认
const handleOperationConfirm = () => {
  if (!selectedAsset.value || !operationAmount.value) return

  const asset = selectedAsset.value
  const protocol = selectedChain.value === 'bitcoin' ? 'btc' : 'brc20'
  const amount = operationAmount.value

  let route = '/wallet/asset?'
  const params = new URLSearchParams()

  switch (operationType.value) {
    case 'send':
      params.set('type', selectedChain.value === 'bitcoin' ? 'l1_send' : 'l2_send')
      break
    case 'deposit':
      params.set('type', 'deposit')
      break
    case 'withdraw':
      params.set('type', 'withdraw')
      break
    case 'lock':
      params.set('type', 'lock')
      break
    case 'unlock':
      params.set('type', 'unlock')
      if (asset.chanId) {
        params.set('chanId', asset.chanId)
      }
      break
    case 'splicing_in':
      params.set('type', 'splicing_in')
      break
    case 'splicing_out':
      params.set('type', 'splicing_out')
      if (asset.chanId) {
        params.set('chanId', asset.chanId)
      }
      break
  }

  params.set('p', protocol)
  params.set('t', asset.type)
  params.set('a', asset.id)
  params.set('amount', amount)

  // 重置状态
  selectedAsset.value = null
  operationType.value = ''
  operationAmount.value = ''
  showDialog.value = false

  // 导航到资产操作页面
  console.log('submit routerUrl',`${route}${params.toString()}`)
// 添加网络切换监听
  router.push(`${route}${params.toString()}`)
}


watch(selectedTranscendingMode, async (newVal) => {
  selectedChain.value = 'bitcoin'
  selectedAssetType.value = 'BTC'

  try {
    if (newVal === 'lightning') {
      await nextTick()
      await channelStore.getAllChannels()
    }
  } catch (error) {
    console.error('Channel fetch error:', error)
  }
})
</script>
