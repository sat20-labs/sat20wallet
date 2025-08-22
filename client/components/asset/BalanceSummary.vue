<template>
  <div class="space-y-4 relative mt-2">
    <!-- Total Balance -->
    <div class="text-center relative group">
      <p class="text-base font-bold text-zinc-500">{{ $t('balanceSummary.totalBalance') }}</p>
      <h2 class="text-3xl font-semibold text-zinc-300" @mouseenter="balanceMouseEnter"
        @mouseleave="showDetails = false">
        {{ formatBalance(btcBalance.total, props.selectedChain, network) }}
      </h2>

      <!-- Balance Details (显示在悬停时) -->
      <div v-if="showDetails"
        class="absolute left-1/2 transform -translate-x-1/2 w-60 mt-2 p-4 bg-zinc-800 border border-zinc-700 rounded-lg shadow-lg space-y-2 z-10">
        <div class="flex justify-between">
          <span class="text-sm text-muted-foreground">{{ $t('balanceSummary.available') }}</span>
          <span class="text-sm text-zinc-400">{{ formatBalance(abailableSats.availableAmt, props.selectedChain, network)
          }}</span>
        </div>
        <div class="flex justify-between">
          <span class="text-sm text-muted-foreground">{{ $t('balanceSummary.unavailable') }}</span>
          <span class="text-sm text-zinc-400">{{ formatBalance(abailableSats.lockedAmt, props.selectedChain, network) }}</span>

        </div>
        <div class="flex justify-between">
          <span class="text-sm text-muted-foreground">{{ $t('balanceSummary.total') }}</span>
          <span class="text-sm text-zinc-400">{{ formatBalance(btcBalance.total, props.selectedChain, network) }}</span>
        </div>

      </div>

      <!-- Action Buttons -->
      <div class="grid grid-cols-4 gap-2 mt-4">
        <Button v-for="button in filteredButtons" :key="button.label" variant="outline"
          class="flex flex-col h-16 items-center py-2 bg-zinc-700/40 hover:bg-zinc-700 border border-zinc-700 rounded-lg"
          :disabled="button.disabled" @click="handleAction(button.action)">
          <Icon :icon="button.icon" class="w-5 h-5 mb-1" />
          <span class="text-xs text-zinc-300">{{ $t(`balanceSummary.${button.label}`) }}</span>
        </Button>
      </div>

      <!-- Asset Operation Dialog -->
      <AssetOperationDialog v-model:open="showDialog" :title="translatedOperationTitle"
        :description="operationDescription" :amount="operationAmount" :address="operationAddress" :chain="selectedChain"
        :max-amount="maxAmount" :operation-type="operationType" :asset-type="selectedAsset?.type"
        :asset-ticker="selectedAsset?.label" :asset-key="selectedAsset?.key" @update:amount="operationAmount = $event"
        @update:address="operationAddress = $event" @confirm="handleOperationConfirm" />
    </div>

    <!-- Receive Address Dialog -->
    <ReceiveQRcode v-if="showReceiveDialog" :address="receiveAddress" :chain="selectedChain"
      @close="showReceiveDialog = false" />

  </div>

</template>

<script setup lang="ts">
import { useTranscendingModeStore } from '@/store'
import { ref, computed, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { useL1Store, useL2Store, useWalletStore } from '@/store'
import { useChannelStore } from '@/store/channel'
import { useAssetActions } from '@/composables/useAssetActions'
import AssetOperationDialog from '@/components/wallet/AssetOperationDialog.vue'
import ReceiveQRcode from '@/components/wallet/ReceiveQRCode.vue'
import { useToast } from '@/components/ui/toast-new'
import { Chain } from '@/types/index'
import { useGlobalStore } from '@/store/global'
import { useI18n } from 'vue-i18n'
import stp from '@/utils/stp'
import { useQuery } from '@tanstack/vue-query'

const { toast } = useToast()
const l1Store = useL1Store()
const l2Store = useL2Store()
const walletStore = useWalletStore()
const channelStore = useChannelStore()

const { deposit, withdraw, splicingIn, splicingOut, unlockUtxo, lockUtxo, l1Send, l2Send, handleError } = useAssetActions()

const showReceiveDialog = ref(false)
const receiveAddress = ref('') // QRCode address
// Props
const props = defineProps<{
  selectedChain: string | 'bitcoin' | 'channel' | 'satoshinet'
  mempoolUrl: string
}>()

// Dialog State
const showDialog = ref(false)
const operationAmount = ref('')
const operationAddress = ref('')
const transcendingModeStore = useTranscendingModeStore()
const operationType = ref<OperationType | undefined>()
const selectedAsset = ref<any>(null)
const { selectedTranscendingMode } = storeToRefs(transcendingModeStore)
const { address, network } = storeToRefs(walletStore)
const abailableSats = ref<{
  availableAmt: number,
  lockedAmt: number
}>({
  availableAmt: 0,
  lockedAmt: 0
})
const { channel } = storeToRefs(channelStore)

type OperationType =
  | 'send'
  | 'deposit'
  | 'withdraw'
  | 'lock'
  | 'unlock'
  | 'splicing_in'
  | 'splicing_out'

// 按钮配置
const buttons = [
  { label: 'Receive', icon: 'lucide:qr-code', action: 'receive', modes: ['poolswap', 'lightning'], chains: ['Bitcoin', 'SatoshiNet'] },
  { label: 'Send', icon: 'lucide:send', action: 'send', modes: ['poolswap', 'lightning'], chains: ['Bitcoin', 'Channel', 'SatoshiNet'] },
  { label: 'Deposit', icon: 'lucide:arrow-down-right', action: 'deposit', modes: ['poolswap'], chains: ['Bitcoin'] },
  { label: 'Withdraw', icon: 'lucide:arrow-up-right', action: 'withdraw', modes: ['poolswap'], chains: ['SatoshiNet'] },
  { label: 'Splicing in', icon: 'lets-icons:sign-in-squre', action: 'splicing_in', modes: ['lightning'], chains: ['Bitcoin'] },
  { label: 'Splicing out', icon: 'lets-icons:sign-out-squre', action: 'splicing_out', modes: ['lightning'], chains: ['Channel'] },
  { label: 'Lock', icon: 'lucide:lock', action: 'lock', modes: ['lightning'], chains: ['SatoshiNet'] },
  { label: 'Unlock', icon: 'lucide:unlock', action: 'unlock', modes: ['lightning'], chains: ['Channel'] },
  { label: 'History', icon: 'lucide:clock', action: 'history', modes: ['poolswap', 'lightning'], chains: ['Bitcoin', 'SatoshiNet', 'Channel'] },
]

// 查询方法
const fetchAbailableSats = async () => {
  if (!address.value) {
    return { availableAmt: 0, lockedAmt: 0 }
  }
  if (props.selectedChain.toLowerCase() === 'channel') {
    // 通道模式下，availableAmt 为 totalSats，lockedAmt 为 0
    return { availableAmt: channelStore.totalSats, lockedAmt: 0 }
  }
  const handler = props.selectedChain.toLowerCase() === 'bitcoin' ? stp.getAssetAmount : stp.getAssetAmount_SatsNet
  const [err, res] = await handler.bind(stp)(address.value, '::')
  console.log('fetchAbailableSats', err, res);
  if (err || !res) {
    return { availableAmt: 0, lockedAmt: 0 }
  }
  return {
    availableAmt: res.availableAmt,
    lockedAmt: res.lockedAmt
  }
}

// useQuery 定时获取
const { data: abailableSatsQuery, refetch: refetchAbailableSats } = useQuery({
  queryKey: [
    'abailableSats',
    address,
    computed(() => props.selectedChain)
  ],
  queryFn: fetchAbailableSats,
  refetchInterval: 5000,
  enabled: computed(() => !!address.value && props.selectedChain.toLowerCase() !== 'channel'),
  initialData: { availableAmt: 0, lockedAmt: 0 },
})
console.log('abailableSatsQuery', abailableSatsQuery);

watch(abailableSatsQuery, (val) => {
  console.log('abailableSatsQuery', val);
  if (val) abailableSats.value = val
}, { immediate: true, deep: true })

// balanceMouseEnter 立即刷新
const balanceMouseEnter = async () => {
  await nextTick()
  showDetails.value = true;
  refetchAbailableSats()
}

const selectedChain = (props.selectedChain || 'bitcoin').toLowerCase()
if (!selectedTranscendingMode.value || !props.selectedChain) {
  console.warn('Props missing: selectedTranscendingMode or selectedChain is undefined. Using default values.')
}
// 过滤按钮
const filteredButtons = computed(() => {
  return buttons.map(button => {
    const isDisabled =
      button.action.toLowerCase() === 'send' &&
      selectedTranscendingMode.value === 'lightning' &&
      props.selectedChain.toLowerCase() === 'channel'

    return {
      ...button,
      disabled: isDisabled,
    }
  }).filter(
    button =>
      button.modes.includes(selectedTranscendingMode.value) &&
      button.chains.map(chain => chain.toLowerCase()).includes(selectedChain)
  )
})

const { t } = useI18n()

// Computed Properties
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
const showAddress = computed(() => {
  if (selectedChain === 'bitcoin') {
    return address.value
  } else if (selectedChain === 'channel') {
    return channel.value?.channelId || address.value // 显示通道ID(address)
  } else if (selectedChain === 'satoshinet') {
    return address.value
  }
  return address.value // 默认返回空字符串
})
const maxAmount = computed(() => {
  if (!selectedAsset.value) return ''
  const asset = selectedAsset.value
  if (asset.type === '*') {
    return Number(abailableSats.value?.availableAmt).toString()
  }
  return Number(asset.amount).toString()
})
console.log('maxAmount', selectedAsset, maxAmount, abailableSats);

const operationDescription = computed(() => {
  if (!selectedAsset.value) return ''
  const asset = selectedAsset.value
  if (asset.type === '*') {
    return `BTC: ${Number(abailableSats.value?.availableAmt).toString()} ${asset.label || 'sats'}`
  }
  const type = asset.type || 'BTC'
  const amount = asset.amount || 0
  return `${type}: ${Number(amount).toString()} ${asset.label || 'sats'}`
})

// Handle Action
const handleAction = (action: string) => {
  const asset = btcBalance.value.assets[0] // Assume the first asset is selected
  

  if (action === 'receive') {
    receiveAddress.value = address.value ?? '' // Use the address from the store or fallback to an empty string
    showReceiveDialog.value = true
    return
  }

  if (action === 'history') {
    if (mempoolUrl.value) {
      window.open(mempoolUrl.value, '_blank') // Open mempoolUrl in a new tab
    } else {
      handleError('Mempool URL is not available')
    }
    return
  }
  if (action === 'send' && !asset) {
    handleError('No asset selected')
    return
  }
  console.log('action', action);
  console.log('action', asset);

  selectedAsset.value = asset
  operationType.value = action as OperationType

  operationAmount.value = '' // 重置金额
  operationAddress.value = '' // 重置地址
  showDialog.value = true
}

// Handle Operation Confirm
const handleOperationConfirm = async () => {
  if (!selectedAsset.value || !operationAmount.value) {
    toast({ title: 'Error', description: 'Please enter a valid amount', variant: 'destructive', duration: 600 })
    return
  }

  if (operationType.value === 'send' && !operationAddress.value) {
    toast({ title: 'Error', description: 'Please enter a valid address', variant: 'destructive', duration: 600 })
    return
  }

  const asset = selectedAsset.value
  const amount = operationAmount.value
  const toAddress = operationType.value === 'send' ? operationAddress.value : address.value
  const chainid = channel.value?.channelId

  try {
    switch (operationType.value) {
      case 'send':
        if (props.selectedChain === 'bitcoin') {
          await l1Send({ toAddress, asset_name: asset.id, amt: amount })
        } else {
          await l2Send({ toAddress, asset_name: asset.id, amt: amount })
        }
        break
      case 'deposit':
        await deposit({ toAddress, asset_name: asset.key, amt: amount, utxos: [], fees: [], })
        break
      case 'withdraw':
        await withdraw({ toAddress, asset_name: asset.id, amt: amount, utxos: [], fees: [] })
        break
      case 'splicing_in':
        await splicingIn({ chanid: chainid, amt: amount, asset_name: asset.id })
        break
      case 'splicing_out':
        await splicingOut({ chanid: chainid, toAddress, amt: amount, asset_name: asset.id })
        break
      case 'lock':
        await lockUtxo({ chanid: chainid, amt: amount, asset_name: asset.id })
        break
      case 'unlock':
        await unlockUtxo({ chanid: chainid, amt: amount, asset_name: asset.id })
        break
      default:
        handleError('Unsupported operation')
    }

    // Reset dialog state
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

// BTC Balance
console.log('btcBalance', props.selectedChain);

const btcBalance = computed(() => {
  const store = props.selectedChain.toLowerCase() === 'bitcoin' ? l1Store : props.selectedChain.toLowerCase() === 'channel' ? channelStore : l2Store
  const btcAssets = store.plainList || []
  return { total: store.totalSats, assets: btcAssets }
})

// Format Balance
const formatBalance = (balance: number | string, chain: string, _network: string) => {
  const numericBalance = typeof balance === 'string' ? parseFloat(balance) : balance
  const formattedBalance = (numericBalance / 1e8).toFixed(8)
  //const unit = chain.toLowerCase() === 'bitcoin' ? 'tBTC' : chain.toLowerCase() === 'channel' ? 'cBTC' : 'sBTC'
  const unit = _network === 'testnet' ? 'tBTC' : 'BTC'
  return `${formattedBalance} ${unit}`
}


// 控制详细信息的显示
const showDetails = ref(false)
// watch(
//   () => props.assets,
//   (newAssets) => {
//     console.log('New assets:', newAssets)

//     const available = newAssets
//       .filter(asset => asset.status === 'available')
//       .reduce((sum, asset) => sum + asset.amount, 0)

//     const unavailable = newAssets
//       .filter(asset => asset.status === 'unavailable')
//       .reduce((sum, asset) => sum + asset.amount, 0)

//     availableBalance.value = available
//     unavailableBalance.value = unavailable
//     totalBalance.value = available + unavailable

//     console.log('Available Balance:', availableBalance.value)
//     console.log('Unavailable Balance:', unavailableBalance.value)
//     console.log('Total Balance:', totalBalance.value)
//   },
//   { immediate: true }
// )

const globalStore = useGlobalStore()

const { env } = storeToRefs(globalStore)

const mempoolUrl = computed(() => {
  if (props.selectedChain === 'bitcoin') {
    return generateMempoolUrl({
      network: network.value,
      path: `address/${showAddress.value}`,
    })
  } else if (props.selectedChain === 'channel') {
    return generateMempoolUrl({
      network: network.value,
      path: `address/${showAddress.value}`,
      chain: Chain.BTC,
      env: env.value,
    })
  } else if (props.selectedChain === 'satoshinet') {
    return generateMempoolUrl({
      network: network.value,
      path: `address/${showAddress.value}`,
      chain: Chain.SATNET,
      env: env.value,
    })
  }
  return '' // 默认返回空字符串，防止未匹配的情况
})
</script>

<style scoped>
.text-muted-foreground {
  color: rgba(255, 255, 255, 0.6);
}

.text-primary {
  color: #4f46e5;
}

.text-foreground {
  color: #ffffff;
}
</style>