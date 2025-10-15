<template>
  <div class="space-y-4 w-full">

    <Tabs defaultValue="l1" v-model="selectedChain" @update:model-value="handleTabChange" class="w-full">
      <TabsList class="grid w-full grid-cols-2 mb-4 bg-zinc-700/90">
        <TabsTrigger value="l1">
          <Icon icon="cryptocurrency:btc" class="w-4 h-4 mr-1 justify-self-center" />
          Bitcoin
        </TabsTrigger>
        <TabsTrigger value="l2">
          <Icon icon="lucide:globe-lock" class="w-4 h-4 mr-1 justify-self-center" />
          SatoshiNet
        </TabsTrigger>
      </TabsList>

      <TabsContent value="l1">
        <L1Card v-model:selectedType="selectedAssetType" :assets="filteredAssets"
          @send="handleSend" @deposit="handleDeposit" />
      </TabsContent>

      <TabsContent value="l2">
        <L2Card v-model:selectedType="selectedAssetType" :assets="filteredAssets"
          @send="handleSend" @withdraw="handleWithdraw" />
      </TabsContent>
    </Tabs>

    <!-- Asset Operation Dialog -->
    <AssetOperationDialog v-model:open="showDialog" :title="translatedOperationTitle"
      :description="operationDescription" :amount="operationAmount" :chain="selectedChain" :address="operationAddress"
      :operation-type="operationType" :max-amount="selectedAsset?.amount" :asset-type="selectedAsset?.type"
      :asset-ticker="selectedAsset?.label" :asset-key="selectedAsset?.key" @update:amount="operationAmount = $event"
      @update:address="operationAddress = $event" @confirm="handleOperationConfirm" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useL1Assets } from '@/composables/hooks/useL1Assets'
import { useL2Assets } from '@/composables/hooks/useL2Assets'
import { Icon } from '@iconify/vue'
import { Label } from '@/components/ui/label'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import L1Card from '@/components/wallet/L1Card.vue'
import L2Card from '@/components/wallet/L2Card.vue'
import walletManager from '@/utils/sat20'
import AssetOperationDialog from '@/components/wallet/AssetOperationDialog.vue'
import { useL1Store, useL2Store } from '@/store'
import { useToast } from '@/components/ui/toast-new'
import { useWalletStore } from '@/store'
import { storeToRefs } from 'pinia'
import { useI18n } from 'vue-i18n'

const { refreshL1Assets } = useL1Assets()
const { refreshL2Assets } = useL2Assets()
const walletStore = useWalletStore()
const { toast } = useToast()

  const props = defineProps({
  modelValue: {
    type: String,
    required: true,
  },
})

type OperationType =
  | 'send'
  | 'deposit'
  | 'withdraw'

const loading = ref(false)
const { address, btcFeeRate } = storeToRefs(walletStore)

// 状态管理
type ChainType = 'l1' | 'l2'

const selectedChain = ref<ChainType>('l1')
const selectedAssetType = ref('ORDX')

// 将父组件的 v-model 值与子组件内部的选项进行映射（统一使用 items 的 value）
const parentToChildTabMap: Record<string, ChainType> = {
  l1: 'l1',
  l2: 'l2',
}

// 同步父组件传入的当前 Tab 到本地 selectedChain（首次与后续变化均生效）
watch(
  () => props.modelValue,
  (newVal) => {
    const key = String(newVal || '').toLowerCase()
    const mapped = (parentToChildTabMap as Record<string, ChainType>)[key] || 'l1'
    if (selectedChain.value !== mapped) {
      selectedChain.value = mapped
    }
  },
  { immediate: true }
)

// Store
const l1Store = useL1Store()
const l2Store = useL2Store()
// 资产列表
const filteredAssets = computed(() => {
  let assets: any[] = []

  // 获取当前链的资产列表
  const getChainAssets = (isMainnet: boolean) => {
    console.log('getChainAssets', isMainnet)

    const store = isMainnet ? l1Store : l2Store
    console.log('selectedAssetType.value', selectedAssetType.value)
    console.log(store);

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
  const isMainnet = selectedChain.value === 'l1'
  assets = getChainAssets(isMainnet)

  console.log('Filtered Assets:', assets)
  return assets
})

// Dialog State
const showDialog = ref(false)
const operationAmount = ref('')
const operationAddress = ref('')
const operationType = ref<OperationType | undefined>()
const selectedAsset = ref<any>(null)

const { t } = useI18n()

const translatedOperationTitle = computed(() => {
  switch (operationType.value) {
    case 'send':
      return t('assetOperationDialog.sendAsset')
    case 'deposit':
      return t('assetOperationDialog.depositAsset')
    case 'withdraw':
      return t('assetOperationDialog.withdrawAsset')
    default:
      return t('assetOperationDialog.assetOperation')
  }
})

const operationDescription = computed(() => {
  if (!selectedAsset.value) return ''
  const asset = selectedAsset.value
  const type = asset.type || 'BTC'
  const amount = asset.amount || 0
  return `${type}: ${Number(amount).toLocaleString()} ${asset.label || 'sats'}`
})

// 事件处理
const emit = defineEmits<{
  (e: 'send', asset: any): void
  (e: 'deposit', asset: any): void
  (e: 'withdraw', asset: any): void
  (e: 'update:model-value', value: string): void
}>()

const changeTab = (newValue: string) => {
  emit('update:model-value', String(newValue)) // 触发事件以更新父组件的值
}

const handleTabChange = (value: string | number) => {
  changeTab(String(value)); // 调用 changeTab 函数来发出事件
  selectedAssetType.value = 'ORDX' // 重置为初始值
}
watch(selectedChain, () => {
  selectedAssetType.value = 'ORDX' // 重置为初始值
})

const handleSend = (asset: any) => {
  // console.log('TranscendingMode - Send:', asset)
  operationType.value = 'send'
  selectedAsset.value = asset
  operationAmount.value = ''
  showDialog.value = true
}

const handleDeposit = (asset: any) => {
  // console.log('TranscendingMode - Deposit:', asset)
  operationType.value = 'deposit'
  selectedAsset.value = asset
  operationAmount.value = ''
  showDialog.value = true
}

const handleWithdraw = (asset: any) => {
  // console.log('TranscendingMode - Withdraw:', asset)
  operationType.value = 'withdraw'
  selectedAsset.value = asset
  operationAmount.value = ''
  showDialog.value = true
}


// 错误处理函数
const handleError = (message: string) => {
  toast({
    title: 'Error',
    description: message,
    variant: 'destructive'
  })
}

// L1 发送操作
const l1Send = async ({ toAddress, asset_name, amt }: any) => {
  loading.value = true

  const [err] = await walletManager.sendAssets(toAddress, asset_name, amt, btcFeeRate.value)
  if (err) {
    toast({
      title: 'Error',
      description: err.message,
      variant: 'destructive',
      duration: 1500,
    })
    loading.value = false
    return
  }

  loading.value = false
  refreshL1Assets()
  toast({
    title: 'Success',
    description: 'send success',
    variant: 'success',
    duration: 1500,
  })
}

// L2 发送操作
const l2Send = async ({ toAddress, asset_name, amt }: any) => {
  loading.value = true
  const [err] = await walletManager.sendAssets_SatsNet(
    toAddress,
    asset_name,
    amt
  )
  if (err) {
    toast({
      title: 'Error',
      description: err.message,
      variant: 'destructive',
      duration: 1500,
    })
    loading.value = false
    return
  }

  loading.value = false
  refreshL2Assets()
  toast({
    title: 'Success',
    description: 'send success',
    variant: 'success',
    duration: 1500,
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
  const [err] = await walletManager.deposit(
    toAddress,
    asset_name,
    amt,
    utxos,
    fees,
    btcFeeRate.value
  )
  if (err) {
    toast({
      title: 'Error',
      description: err.message,
      variant: 'destructive',
      duration: 1500,
    })
    loading.value = false
    return
  }
  loading.value = false
  refreshL1Assets()
  toast({
    title: 'Success',
    description: 'deposit success',
    variant: 'success',
    duration: 1500,
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
  const [err] = await walletManager.withdraw(
    toAddress,
    asset_name,
    amt,
    utxos,
    fees,
    btcFeeRate.value
  )
  if (err) {
    toast({
      title: 'Error',
      description: err.message,
      variant: 'destructive',
      duration: 1500,
    })
    loading.value = false
    return
  }

  loading.value = false
  refreshL2Assets()
  toast({
    title: 'Success',
    description: 'withdraw success',
    variant: 'success',
    duration: 1500,
  })
}

// 更新 handleOperationConfirm 函数
const handleOperationConfirm = async () => {
  if (!selectedAsset.value || !operationAmount.value) return
  if (operationType.value === 'send' && !operationAddress.value) {
    toast({
      title: 'Error',
      description: 'Please enter address',
      variant: 'destructive'
    })
    return
  }

  const asset = selectedAsset.value
  const amount = operationAmount.value
  const toAddress =
    operationType.value === 'send' ? operationAddress.value : address.value

  try {
    switch (operationType.value) {
      case 'send':
        if (selectedChain.value === 'l1') {
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

watch(selectedChain, async () => {
  selectedAssetType.value = 'ORDX'
})


</script>
