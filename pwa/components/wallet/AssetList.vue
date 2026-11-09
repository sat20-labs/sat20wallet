<template>
  <div class="space-y-4 w-full">

    <Tabs defaultValue="l1" v-model="selectedChain" @update:model-value="handleTabChange" class="w-full">
      <TabsList
        class="grid w-full mb-4 bg-zinc-700/90"
        :class="transcendingModeStore.selectedTranscendingMode === 'lightning' ? 'grid-cols-3' : 'grid-cols-2'"
      >
        <TabsTrigger value="l1">
          <Icon icon="cryptocurrency:btc" class="w-4 h-4 mr-1 justify-self-center" />
          Bitcoin
        </TabsTrigger>
        <TabsTrigger v-if="transcendingModeStore.selectedTranscendingMode === 'lightning'" value="channel">
          <Icon icon="lucide:zap" class="w-4 h-4 mr-1 justify-self-center" />
          {{ $t('walletComponents.channel') }}
        </TabsTrigger>
        <TabsTrigger value="l2">
          <Icon icon="lucide:globe-lock" class="w-4 h-4 mr-1 justify-self-center" />
          SatoshiNet
        </TabsTrigger>
      </TabsList>

      <TabsContent value="l1">
        <L1Card v-model:selectedType="selectedAssetType" :assets="filteredAssets"
          :mode="transcendingModeStore.selectedTranscendingMode"
          @splicing_in="handleSplicingIn" @send="handleSend" @receive="handleRGB11Receive"
          @issue-rgb11="showRGB11Issue = true"
          @import-rgb11="showRGB11Import = true" @deposit="handleDeposit"
          @refresh="refreshL1Assets" />
      </TabsContent>

      <TabsContent v-if="transcendingModeStore.selectedTranscendingMode === 'lightning'" value="channel">
        <ChannelCard v-model:selectedType="selectedAssetType" @splicing_out="handleSplicingOut"
          @unlock="handleUnlock" />
      </TabsContent>

      <TabsContent value="l2">
        <L2Card v-model:selectedType="selectedAssetType" :assets="filteredAssets"
          :mode="transcendingModeStore.selectedTranscendingMode"
          @lock="handleLock" @send="handleSend" @withdraw="handleWithdraw"
          @refresh="refreshL2Assets" />
      </TabsContent>
    </Tabs>

    <!-- Asset Operation Dialog -->
    <AssetOperationDialog v-model:open="showDialog" :title="translatedOperationTitle"
      :description="operationDescription" :amount="operationAmount" :chain="selectedChain" :address="operationAddress"
      :operation-type="operationType" :max-amount="operationMaxAmount" :amount-precision="sendPrecision" :asset-type="selectedAsset?.type"
      :asset-ticker="selectedAsset?.label" :asset-key="selectedAsset?.key" @update:amount="operationAmount = $event"
      @update:address="operationAddress = $event" @confirm="handleOperationConfirm" />
    <LockWithExpandConfirmDialog v-if="pendingLockExpand" v-model:open="showLockExpandDialog"
      :asset-key="pendingLockExpand.assetName" :asset-ticker="pendingLockExpand.assetTicker"
      :requested-amount="pendingLockExpand.amount" :available-amount="pendingLockExpand.availableAmount"
      :btc-fee-rate="btcFeeRate" :busy="assetActionLoading" @confirm="confirmLockWithExpand" />
    <RGB11InvoiceDialog v-model:open="showRGB11Invoice" :asset="rgb11ReceiveAsset" />
    <RGB11SendDialog v-model:open="showRGB11Send" :asset="rgb11SendAsset" @completed="refreshRGB11Assets" />
    <RGB11IssueDialog v-model:open="showRGB11Issue" @completed="refreshRGB11Assets" />
    <RGB11ImportDialog v-model:open="showRGB11Import" @completed="refreshRGB11Assets" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { Icon } from '@iconify/vue'
import { Label } from '@/components/ui/label'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import L1Card from '@/components/wallet/L1Card.vue'
import L2Card from '@/components/wallet/L2Card.vue'
import ChannelCard from '@/components/wallet/ChannelCard.vue'
import walletManager from '@/utils/sat20'
import AssetOperationDialog from '@/components/wallet/AssetOperationDialog.vue'
import LockWithExpandConfirmDialog from '@/components/wallet/LockWithExpandConfirmDialog.vue'
import RGB11InvoiceDialog from '@/components/wallet/RGB11InvoiceDialog.vue'
import RGB11SendDialog from '@/components/wallet/RGB11SendDialog.vue'
import RGB11IssueDialog from '@/components/wallet/RGB11IssueDialog.vue'
import RGB11ImportDialog from '@/components/wallet/RGB11ImportDialog.vue'
import { useChannelStore, useL1Store, useL2Store, useTranscendingModeStore, useGlobalStore } from '@/store'
import { useToast } from '@/components/ui/toast-new'
import { useWalletStore } from '@/store'
import { storeToRefs } from 'pinia'
import { useI18n } from 'vue-i18n'
import { validateSendDispatch } from '@/utils/sendAmount'
import { useAssetActions } from '@/composables/useAssetActions'
import { useQueryClient } from '@tanstack/vue-query'
import { walletRequestSessionGuard } from '@/lib/walletSession'

const queryClient = useQueryClient()
const walletStore = useWalletStore()
const globalStore = useGlobalStore()
const { toast } = useToast()
const channelStore = useChannelStore()
const transcendingModeStore = useTranscendingModeStore()
const {
  splicingIn: runSplicingIn,
  splicingOut: runSplicingOut,
  unlockUtxo,
  lockUtxo,
  lockUtxoWithExpand,
  loading: assetActionLoading,
} = useAssetActions()

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
  | 'lock'
  | 'unlock'
  | 'splicing_in'
  | 'splicing_out'

const loading = ref(false)
const { address, btcFeeRate } = storeToRefs(walletStore)

const refreshL1Assets = () => {
  queryClient.invalidateQueries({ queryKey: ['summary-l1'] })
  queryClient.invalidateQueries({ queryKey: ['ns-l1'] })
  queryClient.invalidateQueries({ queryKey: ['rgb11-state'] })
}

const refreshRGB11Assets = async () => {
  await queryClient.invalidateQueries({ queryKey: ['rgb11-state'] })
  await queryClient.invalidateQueries({ queryKey: ['summary-l1'] })
}

const refreshL2Assets = () => {
  queryClient.invalidateQueries({ queryKey: ['summary-l2'] })
}

// 状态管理
type ChainType = 'l1' | 'channel' | 'l2'

const selectedChain = ref<ChainType>('l1')
const selectedAssetType = ref('ORDX')

// 将父组件的 v-model 值与子组件内部的选项进行映射（统一使用 items 的 value）
const parentToChildTabMap: Record<string, ChainType> = {
  l1: 'l1',
  channel: 'channel',
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
const { channel } = storeToRefs(channelStore)
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
      case 'RGB11':
        return store.rgb11List || []
      default:
        return []
    }
  }
  if (selectedChain.value === 'channel') {
    assets = []
  } else {
    const isMainnet = selectedChain.value === 'l1'
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
const sendPrecision = ref<number | undefined>()
const openedSendScope = ref('')
const sendScope = () => [globalStore.env, walletStore.rootAccountId, walletStore.walletId, walletStore.accountIndex, walletStore.network, walletStore.address, selectedChain.value, transcendingModeStore.selectedTranscendingMode, channel.value?.channelId].join('|')
const currentSendAsset = computed(() => filteredAssets.value.find((item: any) => item.key === selectedAsset.value?.key))
const currentOperationAsset = computed(() => selectedChain.value === 'channel'
  ? channelStore.rgb11List.find((item: any) => item.key === selectedAsset.value?.key)
  : currentSendAsset.value)
const operationMaxAmount = computed(() => selectedAsset.value?.protocol === 'rgb11'
  ? (selectedChain.value === 'l1' ? currentOperationAsset.value?.available_amount : currentOperationAsset.value?.amount)
  : operationType.value === 'send' ? currentSendAsset.value?.amount : selectedAsset.value?.amount)

const showLockExpandDialog = ref(false)
const pendingLockExpand = ref<null | {
  chanid: string
  amount: string
  assetName: string
  assetTicker?: string
  availableAmount: string
}>(null)
const showRGB11Invoice = ref(false)
const rgb11ReceiveAsset = ref<any>(null)
const showRGB11Send = ref(false)
const rgb11SendAsset = ref<any>(null)
const showRGB11Issue = ref(false)
const showRGB11Import = ref(false)

const { t } = useI18n()

const translatedOperationTitle = computed(() => {
  switch (operationType.value) {
    case 'send':
      return t('assetOperationDialog.sendAsset')
    case 'deposit':
      return t('assetOperationDialog.depositAsset')
    case 'withdraw':
      return t('assetOperationDialog.withdrawAsset')
    case 'lock':
      return t('assetOperationDialog.lockAsset')
    case 'unlock':
      return t('assetOperationDialog.unlockAsset')
    case 'splicing_in':
      return t('assetOperationDialog.splicingIn')
    case 'splicing_out':
      return t('assetOperationDialog.splicingOut')
    default:
      return t('assetOperationDialog.assetOperation')
  }
})

const operationDescription = computed(() => {
  if (!selectedAsset.value) return ''
  const asset = selectedAsset.value
  const type = asset.protocol === 'rgb11' ? 'RGB' : asset.type || 'BTC'
  const amount = asset.amount || 0
  return `${type}: ${asset.protocol === 'rgb11' ? String(amount) : Number(amount).toLocaleString()} ${asset.label || 'sats'}`
})

// 事件处理
const emit = defineEmits<{
  (e: 'send', asset: any): void
  (e: 'deposit', asset: any): void
  (e: 'withdraw', asset: any): void
  (e: 'lock', asset: any): void
  (e: 'unlock', asset: any): void
  (e: 'splicing_in', asset: any): void
  (e: 'splicing_out', asset: any): void
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

const handleSend = async (asset: any) => {
  if (loading.value || assetActionLoading.value) return
  if (asset?.protocol === 'rgb11' && selectedChain.value === 'l1') {
    rgb11SendAsset.value = asset
    showRGB11Send.value = true
    return
  }
  // console.log('TranscendingMode - Send:', asset)
  operationType.value = 'send'
  selectedAsset.value = asset
  operationAmount.value = ''
  openedSendScope.value = sendScope()
  sendPrecision.value = asset.protocol === '' || asset.key === '::' ? 0 : undefined
  showDialog.value = true
  if (sendPrecision.value === undefined) {
    const scope = openedSendScope.value
    const [err, result] = await walletManager.getTickerInfo(asset.key)
    if (err || sendScope() !== scope || selectedAsset.value?.key !== asset.key) return
    try {
      const ticker = JSON.parse(result.ticker)
      // Successful SDK ticker JSON omits divisibility when it is exactly zero.
      const precision = ticker.divisibility ?? 0
      if (Number.isInteger(precision) && precision >= 0 && precision <= 64) sendPrecision.value = precision
    } catch { /* Unknown precision keeps sending disabled. */ }
  }
}

const handleRGB11Receive = (asset: any) => {
  rgb11ReceiveAsset.value = asset
  showRGB11Invoice.value = true
}

// The market owns RGB registration and deployment. These read-only checks
// reuse its existing wallet APIs; no registration or deployment is started here.
const requireRGB11PublicContract = async (asset: any, operation: OperationType, check: () => void) => {
  const [listError, deployed] = await walletManager.getDeployedContractsInServer()
  check()
  if (listError) throw listError
  const urls = deployed?.contractURLs || []
  // Match the full canonical asset key and retain the SDK's AMM-first selection.
  const url = urls.find((value: unknown) => typeof value === 'string' && value.endsWith(`_${asset.key}_amm.tc`)) ||
    urls.find((value: unknown) => typeof value === 'string' && value.endsWith(`_${asset.key}_transcend.tc`))
  if (!url) throw new Error(t('rgb11Transfer.crossLayerMarketRequired'))
  const [statusError, result] = await walletManager.getDeployedContractStatus(url)
  check()
  if (statusError) throw statusError
  const status = JSON.parse(result?.contractStatus || '{}')
  const [contractChannel, contractAsset, contractType] = url.split('_')
  const name = status.Contract?.assetName
  const current = operation === 'deposit' ? status.currentBlockL1 : status.currentBlock
  const enabled = operation === 'deposit' ? status.enableBlockL1 : status.enableBlock
  if (contractAsset !== asset.key || status.channelAddr !== contractChannel || status.Contract?.contractType !== contractType ||
      name?.Protocol !== 'rgb11' || name.Type !== 'f' || name.Ticker !== asset.ticker ||
      !(status.status >= 100 && status.status < 200) ||
      !Number.isSafeInteger(current) || !Number.isSafeInteger(enabled) || current < enabled) {
    throw new Error(t('rgb11Transfer.crossLayerContractInactive'))
  }
  if (operation === 'deposit') {
    const [exportError, exported] = await walletManager.exportPublicRGB11Contract(asset.ticker)
    check()
    if (exportError) throw exportError
    const publicContract = JSON.parse(exported?.result || '{}')
    if (publicContract.contract_id !== asset.ticker || !publicContract.contract_consignment_base64) {
      throw new Error(t('rgb11Transfer.crossLayerMarketRequired'))
    }
    const [verifyError] = await walletManager.verifyRGB11PublicContract(asset.ticker, publicContract.contract_consignment_base64)
    check()
    if (verifyError) throw verifyError
  }
  // Withdraw intentionally does not export a local L1 contract: an L2-only
  // holder uses the registered L2 ticker and the SDK's final transaction checks.
  return status.divisibility
}

const openAssetOperation = async (type: OperationType, asset: any) => {
  if (loading.value || assetActionLoading.value) return
  operationType.value = type
  selectedAsset.value = asset
  operationAmount.value = ''
  operationAddress.value = ''
  sendPrecision.value = undefined
  openedSendScope.value = sendScope()
  if (asset.protocol !== 'rgb11') { showDialog.value = true; return }
  const scope = openedSendScope.value
  loading.value = true
  try {
    const checkSession = walletRequestSessionGuard('getTickerInfo')
    const check = () => {
      checkSession()
      if (sendScope() !== scope || selectedAsset.value?.key !== asset.key || operationType.value !== type) {
        throw new Error(t('rgb11Transfer.scopeChanged'))
      }
    }
    if (asset.type !== 'f' || !/^rgb11:f:[0-9a-f]{64}$/.test(asset.key)) {
      throw new Error(t('rgb11Transfer.crossLayerFungibleOnly'))
    }
    const destination = type === 'splicing_in' ? channel.value?.address : address.value
    if (!destination) throw new Error(t('approvalUi.addressEmpty'))
    if (type === 'deposit' || type === 'withdraw') {
      const runtimePrecision = await requireRGB11PublicContract(asset, type, check)
      // Before its first deposit, a Transcend runtime may still have provisional
      // precision. Deposit must use the validated local genesis ticker instead.
      sendPrecision.value = type === 'withdraw' ? runtimePrecision : undefined
    } else {
      if (!channel.value || channel.value.status !== 16 || channel.value.pendingSplicing) {
        throw new Error(t('pwaUi.assetActions.channelUnconfirmed'))
      }
      sendPrecision.value = asset.precision
    }
    if (type !== 'withdraw' && sendPrecision.value === undefined) {
      const [error, result] = await walletManager.getTickerInfo(asset.key)
      check()
      if (error) throw error
      const ticker = JSON.parse(result?.ticker || '{}')
      if (ticker.name?.Protocol !== 'rgb11' || ticker.name.Type !== 'f' || ticker.name.Ticker !== asset.ticker) {
        throw new Error(t('assetOperationDialog.amountErrors.unavailable'))
      }
      sendPrecision.value = ticker.divisibility ?? 0
    }
    check()
    operationAddress.value = destination
    showDialog.value = true
  } catch (error) {
    if (sendScope() === scope) handleError(error instanceof Error ? error.message : String(error))
  } finally {
    loading.value = false
  }
}

const handleSplicingIn = (asset: any) => openAssetOperation('splicing_in', asset)
const handleDeposit = (asset: any) => openAssetOperation('deposit', asset)
const handleWithdraw = (asset: any) => openAssetOperation('withdraw', asset)

const handleLock = (asset: any) => {
  if (loading.value || assetActionLoading.value) return
  operationType.value = 'lock'
  selectedAsset.value = asset
  operationAmount.value = ''
  showDialog.value = true
}

const handleUnlock = (asset: any) => {
  if (loading.value || assetActionLoading.value) return
  operationType.value = 'unlock'
  selectedAsset.value = asset
  operationAmount.value = ''
  showDialog.value = true
}

const handleSplicingOut = (asset: any) => openAssetOperation('splicing_out', asset)


// 错误处理函数
const handleError = (message: string) => {
  toast({
    title: t('walletComponents.error'),
    description: message,
    variant: 'destructive'
  })
}

// L1 发送操作
const l1Send = async ({ toAddress, asset_name, amt }: any) => {
  loading.value = true

  // BIP352 is BTC-only. Do not let a generic asset transfer or ordinary
  // Bitcoin-address parser interpret a silent payment address.
  let err: Error | undefined
  if (/^(sp1|tsp1)/i.test(String(toAddress).trim())) {
    if (asset_name !== '::') {
      err = new Error(t('walletComponents.silentBtcOnly'))
    } else {
      [err] = await walletManager.sendSilentPayment(toAddress, amt, btcFeeRate.value)
    }
  } else {
    [err] = await walletManager.sendAssets(toAddress, asset_name, amt, btcFeeRate.value)
  }
  if (err) {
    toast({
      title: t('walletComponents.error'),
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
    title: t('walletComponents.success'),
    description: t('walletComponents.sendSuccess'),
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
      title: t('walletComponents.error'),
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
    title: t('walletComponents.success'),
    description: t('walletComponents.sendSuccess'),
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
      title: t('walletComponents.error'),
      description: err.message,
      variant: 'destructive',
      duration: 1500,
    })
    loading.value = false
    return
  }
  loading.value = false
  refreshL1Assets()
  refreshL2Assets()
  toast({
    title: t('walletComponents.success'),
    description: t(asset_name.startsWith('rgb11:') ? 'rgb11Transfer.crossLayerSubmitted' : 'walletComponents.depositSuccess'),
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
      title: t('walletComponents.error'),
      description: err.message,
      variant: 'destructive',
      duration: 1500,
    })
    loading.value = false
    return
  }

  loading.value = false
  refreshL2Assets()
  refreshL1Assets()
  toast({
    title: t('walletComponents.success'),
    description: t(asset_name.startsWith('rgb11:') ? 'rgb11Transfer.crossLayerSubmitted' : 'walletComponents.withdrawSuccess'),
    variant: 'success',
    duration: 1500,
  })
}

// 更新 handleOperationConfirm 函数
const handleOperationConfirm = async () => {
  if (!selectedAsset.value || !operationAmount.value || loading.value || assetActionLoading.value) return
  const type = operationType.value
  const scope = openedSendScope.value
  if (type === 'send' && !operationAddress.value) {
    toast({
      title: t('walletComponents.error'),
      description: t('walletComponents.enterAddress'),
      variant: 'destructive'
    })
    return
  }

  if (type === 'send' || selectedAsset.value.protocol === 'rgb11') {
    const error = validateSendDispatch(operationAmount.value, sendPrecision.value, operationMaxAmount.value, openedSendScope.value, sendScope())
    if (error) { handleError(t(`assetOperationDialog.amountErrors.${error}`)); return }
  }
  const asset = selectedAsset.value
  const amount = operationAmount.value
  const chanid = channel.value?.channelId
  const toAddress =
    type === 'send' ? operationAddress.value : address.value

  try {
    if (asset.protocol === 'rgb11' && (type === 'deposit' || type === 'withdraw')) {
      loading.value = true
      try {
        const checkSession = walletRequestSessionGuard('getDeployedContractStatus')
        await requireRGB11PublicContract(asset, type, () => {
          checkSession()
          if (scope !== sendScope() || operationType.value !== type || selectedAsset.value?.key !== asset.key) throw new Error(t('rgb11Transfer.scopeChanged'))
        })
      } finally { loading.value = false }
    }
    switch (type) {
      case 'send':
        if (asset?.protocol === 'rgb11' && selectedChain.value === 'l1') {
          throw new Error(t('rgb11Invoice.sendUnavailable'))
        }
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
      case 'splicing_in':
        await runSplicingIn({
          chanid,
          amt: amount,
          asset_name: asset.key,
        })
        break
      case 'splicing_out':
        await runSplicingOut({
          chanid,
          toAddress,
          amt: amount,
          asset_name: asset.key,
        })
        break
      case 'lock':
        const lockResult = await lockUtxo({
          chanid,
          amt: amount,
          asset_name: asset.key,
        })
        if (!lockResult.ok) {
          if (lockResult.expandRequiredAmount !== undefined && chanid) {
            pendingLockExpand.value = {
              chanid,
              amount,
              assetName: asset.key,
              assetTicker: asset.label,
              availableAmount: lockResult.expandRequiredAmount,
            }
            showDialog.value = false
            showLockExpandDialog.value = true
          }
          return
        }
        break
      case 'unlock':
        await unlockUtxo({
          chanid,
          amt: amount,
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
    handleError(error instanceof Error ? error.message : t('walletComponents.operationFailed'))
  }
}

const confirmLockWithExpand = async () => {
  const pending = pendingLockExpand.value
  if (!pending) return
  const succeeded = await lockUtxoWithExpand({
    chanid: pending.chanid,
    amt: pending.amount,
    asset_name: pending.assetName,
    feeRate: btcFeeRate.value,
  })
  if (!succeeded?.ok) return
  showLockExpandDialog.value = false
  pendingLockExpand.value = null
  selectedAsset.value = null
  operationType.value = undefined
  operationAmount.value = ''
  operationAddress.value = ''
}

watch(showLockExpandDialog, (open) => {
  if (!open && !assetActionLoading.value) pendingLockExpand.value = null
})

watch(sendScope, () => {
  if (selectedAsset.value?.protocol === 'rgb11') {
    showDialog.value = false
    selectedAsset.value = null
    operationType.value = undefined
    operationAmount.value = ''
  }
  showRGB11Send.value = false
  showRGB11Invoice.value = false
}, { flush: 'sync' })

watch(selectedChain, async () => {
  selectedAssetType.value = 'ORDX'
})


</script>
