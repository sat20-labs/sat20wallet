<template>
  <div class="space-y-4 relative mt-2">
    <!-- Total Balance -->
    <div class="text-center relative group">
      <p class="text-base font-bold text-zinc-500">{{ $t('balanceSummary.totalBalance') }}</p>
      <h2 class="text-3xl font-semibold text-zinc-300" @mouseenter="balanceMouseEnter"
        @mouseleave="showDetails = false">
        {{ formatMaybeHidden(formatBalance(btcBalance.total, props.selectedChain, network)) }}
      </h2>

      <!-- Balance Details (显示在悬停时) -->
      <div v-if="showDetails"
        class="absolute left-1/2 transform -translate-x-1/2 w-60 mt-2 p-4 bg-zinc-800 border border-zinc-700 rounded-lg shadow-lg space-y-2 z-10">
        <div class="flex justify-between">
          <span class="text-sm text-muted-foreground">{{ $t('balanceSummary.available') }}</span>
          <span class="text-sm text-zinc-400">{{ formatMaybeHidden(formatBalance(abailableSats.availableAmt, props.selectedChain, network))
          }}</span>
        </div>
        <div class="flex justify-between">
          <span class="text-sm text-muted-foreground">{{ $t('balanceSummary.unavailable') }}</span>
          <span class="text-sm text-zinc-400">{{ formatMaybeHidden(formatBalance(btcBalance.total - Number(abailableSats.availableAmt), props.selectedChain, network)) }}</span>

        </div>
        <div class="flex justify-between">
          <span class="text-sm text-muted-foreground">{{ $t('balanceSummary.total') }}</span>
          <span class="text-sm text-zinc-400">{{ formatMaybeHidden(formatBalance(btcBalance.total, props.selectedChain, network)) }}</span>
        </div>

      </div>

      <!-- Action Buttons -->
      <div class="grid grid-cols-4 gap-2 mt-4">
        <Button v-for="button in filteredButtons" :key="button.label" variant="outline"
          class="flex flex-col h-16 items-center py-2 bg-zinc-700/40 hover:bg-zinc-700 border border-zinc-700 rounded-lg"
          @click="handleAction(button.action)">
          <Icon :icon="button.icon" class="w-5 h-5 mb-1" />
          <span class="text-xs text-zinc-300">{{ $t(`balanceSummary.${button.label}`) }}</span>
        </Button>
      </div>

      <!-- Asset Operation Dialog -->
      <AssetOperationDialog v-model:open="showDialog" :title="translatedOperationTitle"
        :description="operationDescription" :amount="operationAmount" :address="operationAddress" :chain="selectedChain"
        :max-amount="maxAmount" :operation-type="operationType" :asset-type="selectedAsset?.type"
        :asset-ticker="selectedAsset?.label" :asset-key="selectedAsset?.key" :network-fee="sendNetworkFee"
        :total-spend="sendTotalSpend" @update:amount="operationAmount = $event"
        @update:address="operationAddress = $event" @confirm="handleOperationConfirm" />
      <LockWithExpandConfirmDialog v-if="pendingLockExpand" v-model:open="showLockExpandDialog"
        :asset-key="pendingLockExpand.assetName" :asset-ticker="pendingLockExpand.assetTicker"
        :requested-amount="pendingLockExpand.amount" :available-amount="pendingLockExpand.availableAmount"
        :btc-fee-rate="btcFeeRate" :busy="assetActionLoading" @confirm="confirmLockWithExpand" />
    </div>

    <!-- Receive Address Dialog -->
    <ReceiveQRcode v-if="showReceiveDialog" :address="receiveAddress" :chain="selectedChain"
      @close="showReceiveDialog = false" />

  </div>

</template>

<script setup lang="ts">
import { useTranscendingModeStore } from '@/store'
import { ref, computed, watch, nextTick } from 'vue'
import { storeToRefs } from 'pinia'
import { openLink } from '@/utils/browser'
import { generateMempoolUrl } from '@/utils'
import { Button } from '@/components/ui/button'
import { Icon } from '@iconify/vue'
import { useChannelStore, useL1Store, useL2Store, useWalletStore } from '@/store'
import { useAssetActions } from '@/composables/useAssetActions'
import AssetOperationDialog from '@/components/wallet/AssetOperationDialog.vue'
import LockWithExpandConfirmDialog from '@/components/wallet/LockWithExpandConfirmDialog.vue'
import ReceiveQRcode from '@/components/wallet/ReceiveQRCode.vue'
import { useToast } from '@/components/ui/toast-new'
import { Chain } from '@/types/index'
import { useGlobalStore } from '@/store/global'
import { useI18n } from 'vue-i18n'
import sat20 from '@/utils/sat20'
import { useQuery } from '@tanstack/vue-query'
import { assetContextKey } from '@/lib/assetContext'
import { validateSendDispatch } from '@/utils/sendAmount'

const { toast } = useToast()
const l1Store = useL1Store()
const l2Store = useL2Store()
const walletStore = useWalletStore()
const channelStore = useChannelStore()
const globalStore = useGlobalStore()

const { deposit, withdraw, splicingIn, splicingOut, unlockUtxo, lockUtxo, lockUtxoWithExpand, l1Send, l2Send, handleError, loading: assetActionLoading } = useAssetActions()

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
const openedSendScope = ref('')
const showLockExpandDialog = ref(false)
const pendingLockExpand = ref<null | {
  chanid: string
  amount: string
  assetName: string
  assetTicker?: string
  availableAmount: string
}>(null)
const { selectedTranscendingMode } = storeToRefs(transcendingModeStore)
const { address, network, btcFeeRate, walletId, accountIndex } = storeToRefs(walletStore)
const { env, hideBalance } = storeToRefs(globalStore)
const abailableSats = ref<{
  availableAmt: string | number,
  lockedAmt: string | number
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
  { label: 'Send', icon: 'lucide:send', action: 'send', modes: ['poolswap', 'lightning'], chains: ['Bitcoin', 'SatoshiNet'] },
  { label: 'Deposit', icon: 'lucide:arrow-down-right', action: 'deposit', modes: ['poolswap'], chains: ['Bitcoin'] },
  { label: 'Withdraw', icon: 'lucide:arrow-up-right', action: 'withdraw', modes: ['poolswap'], chains: ['SatoshiNet'] },
  { label: 'Splicing in', icon: 'lets-icons:sign-in-squre', action: 'splicing_in', modes: ['lightning'], chains: ['Bitcoin'] },
  { label: 'Splicing out', icon: 'lets-icons:sign-out-squre', action: 'splicing_out', modes: ['lightning'], chains: ['Channel'] },
  { label: 'Lock', icon: 'lucide:lock', action: 'lock', modes: ['lightning'], chains: ['SatoshiNet'] },
  { label: 'Unlock', icon: 'lucide:unlock', action: 'unlock', modes: ['lightning'], chains: ['Channel'] },
  { label: 'History', icon: 'lucide:clock', action: 'history', modes: ['poolswap', 'lightning'], chains: ['Bitcoin', 'Channel', 'SatoshiNet'] },
]

// 查询方法
const availableSatsContextKey = computed(() => assetContextKey({
  env: env.value,
  network: network.value,
  chain: props.selectedChain.toLowerCase(),
  walletId: walletId.value,
  accountIndex: accountIndex.value,
  address: address.value || '',
}))
const selectedChain = computed(() => (props.selectedChain || 'bitcoin').toLowerCase())
const sendScope = () => `${walletStore.rootAccountId}|${availableSatsContextKey.value}`
// This entry sends native sats only; other assets use AssetList's precision lookup.
const isNativeSats = (asset: any) => asset?.protocol === '' && asset.key === '::' && asset.id === '::' && asset.type === '*'
// The current SDK's CalcFee_SatsNet returns DEFAULT_FEE_SATSNET for native sends.
const SATOSHINET_NATIVE_SEND_FEE_SATS = 10n
const exactSats = (value: unknown) => {
  if (typeof value === 'number' && (!Number.isSafeInteger(value) || value < 0)) return undefined
  return (typeof value === 'string' || typeof value === 'number') && /^\d+$/.test(String(value))
    ? String(value) : undefined
}
const sendBalanceSnapshot = ref<{ scope: string, availableAmt: string } | null>(null)

const fetchAbailableSats = async () => {
  const contextKey = availableSatsContextKey.value
  const queryAddress = address.value
  if (!queryAddress) throw new Error('Wallet address is unavailable')
  if (props.selectedChain.toLowerCase() === 'channel') {
    return { contextKey, balance: { availableAmt: channelStore.totalSats, lockedAmt: 0 } }
  }
  const handler = props.selectedChain.toLowerCase() === 'bitcoin' ? sat20.getAssetAmount : sat20.getAssetAmount_SatsNet
  const [err, res] = await handler.bind(sat20)(queryAddress, '::')
  console.log('fetchAbailableSats', err, res);
  if (err || !res) {
    throw err || new Error('Asset amount response is empty')
  }
  return {
    contextKey,
    balance: {
      availableAmt: res.availableAmt,
      lockedAmt: res.lockedAmt
    }
  }
}

// useQuery 低频获取，鼠标查看详情时会立即刷新一次。
const { data: abailableSatsQuery, refetch: refetchAbailableSats } = useQuery({
  queryKey: [
    'abailableSats',
    availableSatsContextKey,
  ],
  queryFn: fetchAbailableSats,
  refetchInterval: 60 * 1000,
  enabled: computed(() => !!address.value),
})
console.log('abailableSatsQuery', abailableSatsQuery);

watch(availableSatsContextKey, () => {
  abailableSats.value = { availableAmt: 0, lockedAmt: 0 }
  sendBalanceSnapshot.value = null
}, { flush: 'sync' })

watch(abailableSatsQuery, (val) => {
  console.log('abailableSatsQuery', val);
  if (val?.contextKey === availableSatsContextKey.value) {
    abailableSats.value = val.balance
    const availableAmt = exactSats(val.balance.availableAmt)
    if (availableAmt !== undefined) {
      sendBalanceSnapshot.value = { scope: sendScope(), availableAmt }
    }
  }
}, { immediate: true, deep: true })

// balanceMouseEnter 立即刷新
const balanceMouseEnter = async () => {
  await nextTick()
  showDetails.value = true;
  refetchAbailableSats()
}

const refreshSendBalance = async () => {
  const scope = sendScope()
  const result = await refetchAbailableSats()
  const payload = result?.data
  const availableAmt = payload?.contextKey === availableSatsContextKey.value
    ? exactSats(payload.balance.availableAmt) : undefined
  if (scope !== sendScope() || availableAmt === undefined) return undefined
  sendBalanceSnapshot.value = { scope, availableAmt }
  return availableAmt
}
const exactAvailableSendSats = computed(() => {
  const snapshot = sendBalanceSnapshot.value
  return snapshot?.scope === sendScope() ? snapshot.availableAmt : undefined
})
const availableSendSats = computed(() => {
  const value = exactAvailableSendSats.value
  if (value === undefined) return undefined
  if (selectedChain.value !== 'satoshinet') return value
  const available = BigInt(value)
  return available >= SATOSHINET_NATIVE_SEND_FEE_SATS
    ? String(available - SATOSHINET_NATIVE_SEND_FEE_SATS) : '0'
})
const sendNetworkFee = computed(() => operationType.value === 'send' && selectedChain.value === 'satoshinet'
  ? String(SATOSHINET_NATIVE_SEND_FEE_SATS) : undefined)
const sendTotalSpend = computed(() => {
  if (sendNetworkFee.value === undefined || !/^\d+$/.test(operationAmount.value)) return undefined
  return String(BigInt(operationAmount.value) + SATOSHINET_NATIVE_SEND_FEE_SATS)
})
if (!selectedTranscendingMode.value || !props.selectedChain) {
  console.warn('Props missing: selectedTranscendingMode or selectedChain is undefined. Using default values.')
}
// 过滤按钮
const filteredButtons = computed(() => {
  return buttons.filter(
    button =>
      button.modes.includes(selectedTranscendingMode.value) &&
      button.chains.map(chain => chain.toLowerCase()).includes(selectedChain.value)
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
  if (selectedChain.value === 'bitcoin') {
    return address.value
  } else if (selectedChain.value === 'channel') {
    return channel.value?.channelId || address.value
  } else if (selectedChain.value === 'satoshinet') {
    return address.value
  }
  return address.value // 默认返回空字符串
})
const maxAmount = computed(() => {
  if (operationType.value === 'send') return availableSendSats.value
  if (!selectedAsset.value) return ''
  const asset = selectedAsset.value
  if (asset.type === '*') {
    return String(abailableSats.value.availableAmt)
  }
  return Number(asset.amount).toString()
})
console.log('maxAmount', selectedAsset, maxAmount, abailableSats);

const operationDescription = computed(() => {
  if (!selectedAsset.value) return ''
  const asset = selectedAsset.value
  if (asset.type === '*') {
    return `BTC: ${String(abailableSats.value.availableAmt)} ${asset.label || 'sats'}`
  }
  const type = asset.type || 'BTC'
  const amount = asset.amount || 0
  return `${type}: ${Number(amount).toString()} ${asset.label || 'sats'}`
})

// Handle Action
const handleAction = async (action: string) => {
  const asset = btcBalance.value.assets[0] // Assume the first asset is selected


  if (action === 'receive') {
    receiveAddress.value = address.value ?? '' // Use the address from the store or fallback to an empty string
    showReceiveDialog.value = true
    return
  }

  if (action === 'history') {
    if (mempoolUrl.value) {
      try {
        await openLink(mempoolUrl.value) // 使用统一的链接打开函数
      } catch (error) {
        console.error('打开历史记录链接失败:', error)
        handleError('Failed to open history link')
      }
    } else {
      handleError('Mempool URL is not available')
    }
    return
  }
  if (action === 'send' && (!isNativeSats(asset) || !['bitcoin', 'satoshinet'].includes(selectedChain.value))) {
    handleError('Native sats asset is unavailable')
    return
  }
  if (action === 'send' && sendBalanceSnapshot.value?.scope !== sendScope()) {
    try {
      if (await refreshSendBalance() === undefined) {
        handleError(t('assetOperationDialog.amountErrors.unavailable'))
        return
      }
    } catch {
      handleError(t('assetOperationDialog.amountErrors.unavailable'))
      return
    }
  }
  console.log('action', action);
  console.log('action', asset);

  selectedAsset.value = asset
  operationType.value = action as OperationType
  if (action === 'send') openedSendScope.value = sendScope()

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

  if (operationType.value === 'send') {
    try {
      if (await refreshSendBalance() === undefined) {
        handleError(t('assetOperationDialog.amountErrors.unavailable'))
        return
      }
    } catch {
      handleError(t('assetOperationDialog.amountErrors.unavailable'))
      return
    }
    const error = !isNativeSats(selectedAsset.value) || !isNativeSats(btcBalance.value.assets[0])
      ? 'unavailable'
      : validateSendDispatch(operationAmount.value, 0, availableSendSats.value, openedSendScope.value, sendScope())
    if (error) { handleError(t(`assetOperationDialog.amountErrors.${error}`)); return }
  }

  const asset = selectedAsset.value
  const amount = operationAmount.value
  const chanid = channel.value?.channelId
  const toAddress = operationType.value === 'send' ? operationAddress.value : address.value

  try {
    switch (operationType.value) {
      case 'send':
        if (selectedChain.value === 'bitcoin') {
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
        await splicingIn({ chanid, amt: amount, asset_name: asset.id })
        break
      case 'splicing_out':
        await splicingOut({ chanid, toAddress, amt: amount, asset_name: asset.id })
        break
      case 'lock':
        const lockResult = await lockUtxo({ chanid, amt: amount, asset_name: asset.id })
        if (!lockResult.ok) {
          if (lockResult.expandRequiredAmount !== undefined && chanid) {
            pendingLockExpand.value = {
              chanid,
              amount,
              assetName: asset.id,
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
        await unlockUtxo({ chanid, amt: amount, asset_name: asset.id })
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

// BTC Balance
console.log('btcBalance', props.selectedChain);

const btcBalance = computed(() => {
  const store = props.selectedChain.toLowerCase() === 'bitcoin'
    ? l1Store
    : props.selectedChain.toLowerCase() === 'channel'
      ? channelStore
      : l2Store
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

const formatMaybeHidden = (value: string) => {
  return hideBalance.value ? '••••••••' : value
}

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
