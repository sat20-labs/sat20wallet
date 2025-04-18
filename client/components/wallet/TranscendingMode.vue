<template>
  <div class="space-y-4 w-full">
    <!-- Mode Selection -->
    <div class="mb-4 flex items-center gap-4">
      <Label class="text-sm text-foreground/50 shrink-0">Trans Mode:</Label>
      <div class="flex gap-2">
        <Button
          :variant="
            selectedTranscendingMode === 'poolswap' ? 'secondary' : 'outline'
          "
          class="h-8 w-[100px] flex items-center justify-start px-3 rounded-md"
          @click="selectedTranscendingMode = 'poolswap'"
        >
          <Icon icon="lucide:repeat" class="w-5 h-5 shrink-0" />
          <span class="text-xs">Poolswap</span>
        </Button>
        <Button
          :variant="
            selectedTranscendingMode === 'lightning' ? 'secondary' : 'outline'
          "
          class="h-8 w-[100px] flex items-center justify-start px-3 rounded-md"
          @click="selectedTranscendingMode = 'lightning'"
        >
          <Icon icon="lucide:zap" class="w-5 h-5 shrink-0" />
          <span class="text-xs">Lightning</span>
        </Button>
      </div>
    </div>

    <!-- Poolswap Mode -->
    <div v-if="selectedTranscendingMode === 'poolswap'">
      <Tabs defaultValue="bitcoin" v-model="selectedChain" class="w-full">
        <TabsList class="grid w-full grid-cols-3 mb-4 bg-black/15">
          <TabsTrigger value="bitcoin">
            <Icon
              icon="cryptocurrency:btc"
              class="w-4 h-4 mr-1 justify-self-center"
            />
            Bitcoin
          </TabsTrigger>
          <TabsTrigger value="pool">
            <Icon
              icon="lucide:waves"
              class="w-4 h-4 mr-1 justify-self-center"
            />
            Pool
          </TabsTrigger>
          <TabsTrigger value="satoshinet">
            <Icon
              icon="lucide:globe-lock"
              class="w-4 h-4 mr-1 justify-self-center"
            />
            SatoshiNet
          </TabsTrigger>
        </TabsList>

        <TabsContent value="bitcoin">
          <L1Card
            v-model:selectedType="selectedAssetType"
            :assets="filteredAssets"
            :mode="selectedTranscendingMode"
            @splicing_in="handleSplicingIn"
            @send="handleSend"
            @deposit="handleDeposit"
          />
        </TabsContent>

        <TabsContent value="pool">
          <PoolManager />
        </TabsContent>

        <TabsContent value="satoshinet">
          <L2Card
            v-model:selectedType="selectedAssetType"
            :assets="filteredAssets"
            :mode="selectedTranscendingMode"
            @lock="handleLock"
            @send="handleSend"
            @withdraw="handleWithdraw"
          />
        </TabsContent>
      </Tabs>
    </div>

    <!-- Lightning Mode -->
    <div v-else>
      <Tabs defaultValue="bitcoin" v-model="selectedChain" class="w-full">
        <TabsList class="grid w-full grid-cols-3 mb-4 bg-black/15">
          <TabsTrigger value="bitcoin">
            <Icon
              icon="cryptocurrency:btc"
              class="w-4 h-4 mr-1 justify-self-center"
            />
            Bitcoin
          </TabsTrigger>
          <TabsTrigger value="channel">
            <Icon icon="lucide:zap" class="w-4 h-4 mr-1 justify-self-center" />
            Channel
          </TabsTrigger>
          <TabsTrigger value="satoshinet">
            <Icon
              icon="lucide:globe-lock"
              class="w-4 h-4 mr-1 justify-self-center"
            />
            SatoshiNet
          </TabsTrigger>
        </TabsList>

        <TabsContent value="bitcoin">
          <L1Card
            v-model:selectedType="selectedAssetType"
            :assets="filteredAssets"
            :mode="selectedTranscendingMode"
            @splicing_in="handleSplicingIn"
            @send="handleSend"
            @deposit="handleDeposit"
          />
        </TabsContent>

        <TabsContent value="channel">
          <ChannelCard
            v-model:selectedType="selectedAssetType"
            @splicing_out="handleSplicingOut"
            @unlock="handleUnlock"
          />
        </TabsContent>

        <TabsContent value="satoshinet">
          <L2Card
            v-model:selectedType="selectedAssetType"
            :assets="filteredAssets"
            :mode="selectedTranscendingMode"
            @lock="handleLock"
            @send="handleSend"
            @withdraw="handleWithdraw"
          />
        </TabsContent>
      </Tabs>
    </div>

    <!-- Asset Operation Dialog -->
    <AssetOperationDialog
      v-model:open="showDialog"
      :title="operationTitle"
      :description="operationDescription"
      :amount="operationAmount"
      :address="operationAddress"
      :operation-type="operationType"
      :asset-type="selectedAsset?.type"
      :asset-ticker="selectedAsset?.ticker"
      @update:amount="operationAmount = $event"
      @update:address="operationAddress = $event"
      @confirm="handleOperationConfirm"
    />
  </div>
</template>

<script setup lang="ts">
import { useRouter } from 'vue-router'
import { Icon } from '@iconify/vue'
import { Label } from '@/components/ui/label'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import L1Card from '@/components/wallet/L1Card.vue'
import L2Card from '@/components/wallet/L2Card.vue'
import satsnetStp from '@/utils/stp'
import ChannelCard from '@/components/wallet/ChannelCard.vue'
import PoolManager from '@/components/wallet/PoolManager.vue'
import AssetOperationDialog from '@/components/wallet/AssetOperationDialog.vue'
import { useL1Store, useL2Store } from '@/store'
import { useChannelStore } from '@/store/channel'
import { useToast } from '@/components/ui/toast/use-toast'
import { useWalletStore } from '@/store'
import { sleep } from 'radash'
import { storeToRefs } from 'pinia'

const { refreshL1Assets } = useL1Assets()
const { refreshL2Assets } = useL2Assets()
const channelStore = useChannelStore()
const walletStore = useWalletStore()
const { toast } = useToast()

type OperationType =
  | 'send'
  | 'deposit'
  | 'withdraw'
  | 'lock'
  | 'unlock'
  | 'splicing_in'
  | 'splicing_out'

const loading = ref(false)
const { address, feeRate } = storeToRefs(walletStore)
const { channel } = storeToRefs(channelStore)

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
        return (store.plainList || []).map((asset) => ({
          ...asset,
          type: 'BTC',
        }))
      case 'SAT20':
        return (store.sat20List || []).map((asset) => ({
          ...asset,
          type: 'SAT20',
        }))
      case 'Runes':
        return (store.runesList || []).map((asset) => ({
          ...asset,
          type: 'Runes',
        }))
      case 'BRC20':
        return (store.brc20List || []).map((asset) => ({
          ...asset,
          type: 'BRC20',
        }))
      default:
        return []
    }
  }
  console.log('selectedTranscendingMode.value')
  console.log(selectedTranscendingMode.value)
  console.log('selectedChain.value')
  console.log(selectedChain.value)
  console.log('selectedAssetType.value')
  console.log(selectedAssetType.value)


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
const operationAddress = ref('')
const operationType = ref<OperationType | undefined>()
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

// 错误处理函数
const handleError = (message: string) => {
  toast({
    title: 'Error',
    description: message,
  })
}

// 检查通道状态
const checkChannel = async (chanid: string) => {
  const [err, result] = await satsnetStp.getChannelStatus(chanid)
  console.log(result)

  if (err || result !== 16) {
    return false
  }

  return true
}

// Splicing In 操作
const splicingIn = async ({
  chanid,
  utxos,
  amt,
  feeUtxos = [],
  feeRate,
  asset_name,
}: any): Promise<void> => {
  loading.value = true

  const [err, result] = await satsnetStp.splicingIn(
    chanid,
    asset_name,
    utxos,
    feeUtxos,
    feeRate,
    amt
  )

  if (err) {
    loading.value = false
    handleError(err.message)
    return
  }
  refreshL1Assets()
  await channelStore.getAllChannels()
  loading.value = false
}

// Splicing Out 操作
const splicingOut = async ({
  chanid,
  toAddress,
  amt,
  feeRate,
  asset_name,
}: any): Promise<void> => {
  loading.value = true
  const feeUtxos = l1Store.plainList?.[0]?.utxos || []
  const [err, result] = await satsnetStp.splicingOut(
    chanid,
    toAddress,
    asset_name,
    feeUtxos,
    feeRate,
    amt
  )

  if (err) {
    loading.value = false
    handleError(err.message)
    return
  }
  refreshL1Assets()
  await channelStore.getAllChannels()
  loading.value = false
}

// Unlock UTXO 操作
const unlockUtxo = async ({ chanid, amt, feeUtxos = [], asset_name }: any) => {
  console.log('Unlock UTXO:', chanid, amt, feeUtxos, asset_name)
  loading.value = true
  const status = await checkChannel(chanid)
  if (!status) {
    toast({
      title: 'error',
      description: 'channel tx has not been confirmed',
    })
    loading.value = false
    return
  }

  const [err, result] = await satsnetStp.unlockUtxo(chanid, asset_name, amt, [])
  if (err) {
    toast({
      title: 'error',
      description: err.message,
    })
    loading.value = false
    return
  }
  await sleep(1000)
  await channelStore.getAllChannels()
  loading.value = false
  refreshL2Assets()
  await channelStore.getAllChannels()
  toast({
    title: 'success',
    description: 'unlock success',
  })
}

// Lock UTXO 操作
const lockUtxo = async ({
  utxos,
  chanid,
  amt,
  feeUtxos = [],
  asset_name,
}: any) => {
  loading.value = true
  const [err, result] = await satsnetStp.lockUtxo(
    chanid,
    asset_name,
    amt,
    utxos,
    feeUtxos
  )
  if (err) {
    toast({
      title: 'error',
      description: err.message,
    })
    loading.value = false
    return
  }

  await channelStore.getAllChannels()
  loading.value = false
  refreshL2Assets()
  await channelStore.getAllChannels()
  toast({
    title: 'success',
    description: 'lock success',
  })
}

// L1 发送操作
const l1Send = async ({ toAddress, utxos, amt }: any) => {
  loading.value = true
  const [err, result] = await satsnetStp.sendUtxos(toAddress, utxos, amt)
  if (err) {
    toast({
      title: 'error',
      description: err.message,
    })
    loading.value = false
    return
  }

  loading.value = false
  refreshL1Assets()
  toast({
    title: 'success',
    description: 'send success',
  })
}

// L2 发送操作
const l2Send = async ({ toAddress, asset_name, amt }: any) => {
  loading.value = true
  const [err, result] = await satsnetStp.sendAssetsSatsNet(
    toAddress,
    asset_name,
    amt
  )
  if (err) {
    toast({
      title: 'error',
      description: err.message,
    })
    loading.value = false
    return
  }

  loading.value = false
  refreshL2Assets()
  toast({
    title: 'success',
    description: 'send success',
  })
}

// Deposit 操作
const deposit = async ({
  toAddress,
  asset_name,
  amt,
  utxos = [],
  fees = [],
}: any) => {
  loading.value = true
  const [err, result] = await satsnetStp.deposit(
    toAddress,
    asset_name,
    amt,
    utxos,
    fees,
    feeRate.value
  )
  if (err) {
    toast({
      title: 'error',
      description: err.message,
    })
    loading.value = false
    return
  }
  loading.value = false
  refreshL1Assets()
  await channelStore.getAllChannels()
  toast({
    title: 'success',
    description: 'deposit success',
  })
}

// Withdraw 操作
const withdraw = async ({
  toAddress,
  asset_name,
  amt,
  utxos = [],
  fees = [],
}: any) => {
  loading.value = true
  const [err, result] = await satsnetStp.withdraw(
    toAddress,
    asset_name,
    amt,
    utxos,
    fees,
    feeRate.value
  )
  if (err) {
    toast({
      title: 'error',
      description: err.message,
    })
    loading.value = false
    return
  }

  loading.value = false
  refreshL2Assets()
  await channelStore.getAllChannels()
  toast({
    title: 'success',
    description: 'withdraw success',
  })
}

// 更新 handleOperationConfirm 函数
const handleOperationConfirm = async () => {
  if (!selectedAsset.value || !operationAmount.value) return
  if (operationType.value === 'send' && !operationAddress.value) {
    toast({
      title: 'error',
      description: 'Please enter address',
    })
    return
  }

  const asset = selectedAsset.value
  const chainid = channel.value?.channelId
  const amount = operationAmount.value
  const toAddress =
    operationType.value === 'send' ? operationAddress.value : address.value

  try {
    switch (operationType.value) {
      case 'send':
        if (selectedChain.value === 'bitcoin') {
          await l1Send({
            toAddress,
            utxos: [],
            amt: amount,
          })
        } else {
          await l2Send({
            toAddress,
            asset_name: asset.key,
            amt: amount,
          })
        }
        break
      case 'deposit':
        await deposit({
          toAddress: address.value,
          asset_name: asset.key,
          amt: amount,
          utxos: [],
          fees: [],
        })
        break
      case 'withdraw':
        await withdraw({
          toAddress: address.value,
          asset_name: asset.key,
          amt: amount,
          utxos: [],
          fees: [],
        })
        break
      case 'lock':
        await lockUtxo({
          chanid: chainid,
          utxos: [],
          amt: amount,
          feeUtxos: [],
          asset_name: asset.key,
        })
        break
      case 'unlock':
        await unlockUtxo({
          chanid: chainid,
          amt: amount,
          feeUtxos: [],
          asset_name: asset.key,
        })
        break
      case 'splicing_in':
        await splicingIn({
          chanid: chainid,
          utxos: [],
          amt: amount,
          feeUtxos: [],
          feeRate: feeRate.value,
          asset_name: asset.key,
        })
        break
      case 'splicing_out':
        await splicingOut({
          chanid: chainid,
          toAddress: address.value,
          amt: amount,
          feeRate: feeRate.value,
          asset_name: asset.key,
        })
        break
    }

    // 重置状态
    selectedAsset.value = null
    operationType.value = undefined
    operationAmount.value = ''
    operationAddress.value = ''
    showDialog.value = false
  } catch (error) {
    console.error('Operation error:', error)
    handleError('Operation failed')
  }
}

watch(selectedChain, async (newVal) => {
  selectedAssetType.value = 'BTC'
})
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
