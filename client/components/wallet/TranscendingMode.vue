<template>
  <div class="space-y-4 w-full">
    <!-- Mode Selection -->
    <div class="mb-4 flex items-center gap-4">
      <Label class="text-sm text-foreground/50 shrink-0">Trans Mode:</Label>
      <div class="flex gap-2">
        <Button :variant="selectedTranscendingMode === 'poolswap' ? 'secondary' : 'outline'
          " class="h-8 w-[100px] flex items-center justify-start px-3 rounded-md"
          @click="selectedTranscendingMode = 'poolswap'">
          <Icon icon="lucide:repeat" class="w-5 h-5 shrink-0" />
          <span class="text-xs">Poolswap</span>
        </Button>
        <Button :variant="selectedTranscendingMode === 'lightning' ? 'secondary' : 'outline'
          " class="h-8 w-[100px] flex items-center justify-start px-3 rounded-md"
          @click="selectedTranscendingMode = 'lightning'">
          <Icon icon="lucide:zap" class="w-5 h-5 shrink-0" />
          <span class="text-xs">Lightning</span>
        </Button>
      </div>
    </div>

    <!-- Poolswap Mode -->
    <div v-if="selectedTranscendingMode === 'poolswap'">
      <Tabs defaultValue="bitcoin" v-model="selectedChain" class="w-full">
        <!-- <TabsList class="grid w-full grid-cols-2 mb-4 bg-black/15"> -->
        <TabsList class="grid w-full grid-cols-2 mb-4 bg-zinc-700/90">
          <TabsTrigger value="bitcoin">
            <Icon icon="cryptocurrency:btc" class="w-4 h-4 mr-1 justify-self-center" />
            Bitcoin
          </TabsTrigger>
          <!-- <TabsTrigger value="pool">
            <Icon
              icon="lucide:waves"
              class="w-4 h-4 mr-1 justify-self-center"
            />
            Pool
          </TabsTrigger> -->
          <TabsTrigger value="satoshinet">
            <Icon icon="lucide:globe-lock" class="w-4 h-4 mr-1 justify-self-center" />
            SatoshiNet
          </TabsTrigger>
        </TabsList>

        <TabsContent value="bitcoin">
          <L1Card v-model:selectedType="selectedAssetType" :assets="filteredAssets" :mode="selectedTranscendingMode"
            @splicing_in="handleSplicingIn" @refresh="refreshL1Assets" @send="handleSend" @deposit="handleDeposit" />
        </TabsContent>

        <TabsContent value="pool">
          <PoolManager />
        </TabsContent>

        <TabsContent value="satoshinet">
          <L2Card v-model:selectedType="selectedAssetType" :assets="filteredAssets" :mode="selectedTranscendingMode"
            @lock="handleLock" @refresh="refreshL2Assets" @send="handleSend" @withdraw="handleWithdraw" />
        </TabsContent>
      </Tabs>
    </div>

    <!-- Lightning Mode -->
    <div v-else>
      <Tabs defaultValue="bitcoin" v-model="selectedChain" class="w-full">
        <TabsList class="grid w-full grid-cols-3 mb-4  bg-zinc-700/90">
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
            @splicing_in="handleSplicingIn" @refresh="refreshL1Assets" @send="handleSend" @deposit="handleDeposit" />
        </TabsContent>

        <TabsContent value="channel">
          <ChannelCard v-model:selectedType="selectedAssetType" @splicing_out="handleSplicingOut"
            @unlock="handleUnlock" />
        </TabsContent>

        <TabsContent value="satoshinet">
          <L2Card v-model:selectedType="selectedAssetType" :assets="filteredAssets" :mode="selectedTranscendingMode"
            @lock="handleLock" @refresh="refreshL2Assets" @send="handleSend" @withdraw="handleWithdraw" />
        </TabsContent>
      </Tabs>
    </div>

    <!-- Asset Operation Dialog -->
    <AssetOperationDialog v-model:open="showDialog" :title="operationTitle" :description="operationDescription"
      :amount="operationAmount" :address="operationAddress" :operation-type="operationType"
      :asset-type="selectedAsset?.type" :asset-ticker="selectedAsset?.label" @update:amount="operationAmount = $event"
      @update:address="operationAddress = $event" @confirm="handleOperationConfirm" />
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
import sat20 from '@/utils/sat20'
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
const { address, feeRate, btcFeeRate } = storeToRefs(walletStore)
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
      case 'ORDX':
        return (store.sat20List || []).map((asset) => ({
          ...asset,
          type: 'ORDX',
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
  return `${type}: ${amount} ${asset.label || 'sats'}`
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

// 通用高阶函数，统一处理 loading、toast、错误
const withLoadingToast = async (
  fn: () => Promise<[any, any]>,
  { success, error }: { success: string; error: string }
) => {
  loading.value = true
  try {
    const [err, result] = await fn()
    if (err) {
      toast({ title: 'Error', description: err.message || error })
      return false
    }
    toast({ title: 'Success', description: success })
    return result
  } catch (e) {
    toast({ title: 'Error', description: error })
    return false
  } finally {
    loading.value = false
  }
}

// 检查通道状态
const checkChannel = async (chanid: string) => {
  const [err, result] = await satsnetStp.getChannelStatus(chanid)
  return !err && result === 16
}

// Splicing In 操作
const splicingIn = async (params: any): Promise<void> => {
  await withLoadingToast(
    () => satsnetStp.splicingIn(
      params.chanid,
      params.asset_name,
      params.utxos,
      params.feeUtxos,
      params.feeRate,
      params.amt
    ),
    { success: 'Splicing in successful.', error: 'Splicing in failed.' }
  )
  refreshL1Assets()
  await channelStore.getAllChannels()
}

// Splicing Out 操作
const splicingOut = async (params: any): Promise<void> => {
  const feeUtxos = l1Store.plainList?.[0]?.utxos || []
  await withLoadingToast(
    () => satsnetStp.splicingOut(
      params.chanid,
      params.toAddress,
      params.asset_name,
      feeUtxos,
      params.feeRate,
      params.amt
    ),
    { success: 'Splicing out successful.', error: 'Splicing out failed.' }
  )
  refreshL1Assets()
  await channelStore.getAllChannels()
}

// Unlock UTXO 操作
const unlockUtxo = async (params: any) => {
  if (!(await checkChannel(params.chanid))) {
    toast({
      title: 'Error',
      description: 'Channel transaction has not been confirmed.',
    })
    return
  }
  await withLoadingToast(
    () => satsnetStp.unlockFromChannel(params.chanid, params.asset_name, params.amt, []),
    { success: 'Unlock successful.', error: 'Unlock failed.' }
  )
  await sleep(1000)
  await channelStore.getAllChannels()
  refreshL2Assets()
  await channelStore.getAllChannels()
}

// Lock UTXO 操作
const lockUtxo = async (params: any) => {
  await withLoadingToast(
    () => satsnetStp.lockToChannel(
      params.chanid,
      params.asset_name,
      params.amt,
      params.utxos,
      params.feeUtxos
    ),
    { success: 'Lock successful.', error: 'Lock failed.' }
  )
  await channelStore.getAllChannels()
  refreshL2Assets()
  await channelStore.getAllChannels()
}

// L1 发送操作
const l1Send = async (params: any) => {
  await withLoadingToast(
    () => satsnetStp.sendAssets(params.toAddress, params.asset_name, params.amt, 0),
    { success: 'Send successful.', error: 'Send failed.' }
  )
  refreshL1Assets()
}

// L2 发送操作
const l2Send = async (params: any) => {
  await withLoadingToast(
    () => satsnetStp.sendAssets_SatsNet(params.toAddress, params.asset_name, params.amt),
    { success: 'Send successful.', error: 'Send failed.' }
  )
  refreshL2Assets()
}

// Deposit 操作
const deposit = async (params: any) => {
  await withLoadingToast(
    () => satsnetStp.deposit(
      params.toAddress,
      params.asset_name,
      params.amt,
      params.utxos,
      params.fees,
      btcFeeRate.value
    ),
    { success: 'Deposit successful.', error: 'Deposit failed.' }
  )
  refreshL1Assets()
  await channelStore.getAllChannels()
}

// Withdraw 操作
const withdraw = async (params: any) => {
  await withLoadingToast(
    () => satsnetStp.withdraw(
      params.toAddress,
      params.asset_name,
      params.amt,
      params.utxos,
      params.fees,
      btcFeeRate.value
    ),
    { success: 'Withdraw successful.', error: 'Withdraw failed.' }
  )
  refreshL2Assets()
  await channelStore.getAllChannels()
}

// 更新 handleOperationConfirm 函数
const handleOperationConfirm = async () => {
  if (!selectedAsset.value || !operationAmount.value) return
  if (operationType.value === 'send' && !operationAddress.value) {
    toast({
      title: 'Error',
      description: 'Please enter the address.',
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
            asset_name: asset.key,
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
          feeRate: btcFeeRate.value,
          asset_name: asset.key,
        })
        break
      case 'splicing_out':
        await splicingOut({
          chanid: chainid,
          toAddress: address.value,
          amt: amount,
          feeRate: btcFeeRate.value,
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
    toast({ title: 'Error', description: 'Operation failed.' })
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
