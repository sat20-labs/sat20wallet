<template>
  <div class="space-y-4">
    <!-- Asset Type Tabs -->
    <div class="flex justify-between border-b border-zinc-700 mb-4">
      <nav class="flex -mb-px gap-4">
        <button v-for="(type, index) in assetTypes" :key="index" @click="selectedType = type"
          class="pb-2 px-1 font-mono font-semibold text-sm relative" :class="{
            'text-foreground/90': selectedType === type,
            'text-muted-foreground': selectedType !== type
          }">
          {{ $t(`l1AssetsTabs.assetType.${type}`) }}
          <div class="absolute bottom-0 left-0 right-0 h-0.5 transition-all" :class="{
            'bg-gradient-to-r from-primary to-primary/50 scale-x-100': selectedType === type,
            'scale-x-0': selectedType !== type
          }" />
        </button>
      </nav>
      <div class="flex items-center">
        <Button size="icon" variant="ghost" @click="handlerRefresh">
          <Icon icon="lets-icons:refresh-2-light" class="text-zinc-300 mb-[1px]" />
        </Button>
        <Button size="icon" variant="ghost" as-child>
          <a :href="mempoolUrl" target="_blank" class="mb-[1px] hover:text-primary"
            :title="$t('l1AssetsTabs.viewTradeHistory')">
            <Icon icon="quill:link" class="text-zinc-400 hover:text-primary/90" />
          </a>
        </Button>
      </div>
    </div>

    <!-- Asset Lists -->
    <div class="space-y-2">
      <div v-if="selectedType === 'RGB11'" class="rounded-lg border border-zinc-700 bg-muted/60 p-3 text-xs text-zinc-400">
        <div class="flex justify-between gap-3">
          <span>{{ $t('rgb11Transfer.consistency') }}: {{ rgb11State.consistency_status }}</span>
          <span>{{ $t('rgb11Transfer.dkvs') }}: {{ rgb11State.dkvs_status }}</span>
        </div>
        <div v-if="rgb11State.consistency_status !== 'ok'" class="mt-1 text-amber-500">
          {{ $t('rgb11Transfer.inconsistentWarning') }}
        </div>
        <div class="mt-1" :class="rgb11State.auto_backup_enabled ? 'text-emerald-400' : 'text-amber-500'">
          {{ rgb11State.auto_backup_enabled
            ? $t('rgb11Transfer.autoBackupEnabled')
            : $t('rgb11Transfer.manualBackupRequired') }}
        </div>
        <div class="mt-3 grid grid-cols-2 gap-2">
          <Button size="sm" variant="outline" @click="emit('issue-rgb11')">
            <Icon icon="lucide:badge-plus" class="mr-2 h-4 w-4" />
            {{ $t('rgb11Transfer.issue') }}
          </Button>
          <Button size="sm" variant="outline" @click="emit('import-rgb11')">
            <Icon icon="lucide:file-input" class="mr-2 h-4 w-4" />
            {{ $t('rgb11Transfer.import') }}
          </Button>
        </div>
        <div class="mt-2 grid grid-cols-2 gap-2">
          <Button size="sm" variant="outline" :disabled="syncing" @click="backupRGB11State">
            {{ rgb11State.auto_backup_enabled ? $t('rgb11Transfer.backupNow') : $t('rgb11Transfer.enableAutoBackup') }}
          </Button>
          <Button size="sm" variant="outline" :disabled="syncing" @click="restoreRGB11State">
            {{ $t('rgb11Transfer.restore') }}
          </Button>
        </div>
        <div v-if="syncMessage" class="mt-2 break-all" :class="syncError ? 'text-red-400' : 'text-emerald-400'">
          {{ syncMessage }}
        </div>
        <div class="mt-3 border-t border-zinc-700/70 pt-3">
          <div class="mb-2 flex items-center justify-between">
            <span class="font-medium text-zinc-300">{{ $t('rgb11Transfer.monitor') }}</span>
            <span>{{ rgb11Transfers.length }}</span>
          </div>
          <div v-if="!rgb11Transfers.length" class="text-zinc-500">
            {{ $t('rgb11Transfer.noTransfers') }}
          </div>
          <div v-else class="space-y-2">
            <div v-for="transfer in rgb11Transfers" :key="transfer.transfer_id"
              class="rounded border border-zinc-700/60 p-2">
              <div class="flex items-center justify-between gap-2">
                <span>{{ transfer.direction }} · {{ transfer.asset?.Name?.Ticker || 'RGB11' }}</span>
                <span :class="rgb11TransferStatusClass(transfer.status)">{{ transfer.status }}</span>
              </div>
              <div>{{ $t('rgb11Transfer.ackStatus') }}: {{ transfer.ack_status || 'pending' }}</div>
              <div v-if="transfer.reject_reason" class="text-red-400">
                {{ $t('rgb11Transfer.rejectReason') }}: {{ transfer.reject_reason }}
              </div>
              <div>{{ $t('rgb11Transfer.durability') }}: {{ transfer.relay_durability || 'local' }}</div>
              <div v-if="transfer.witness_txid" class="break-all">
                TX: {{ transfer.witness_txid }}
              </div>
              <div v-else class="break-all">ID: {{ transfer.transfer_id }}</div>
            </div>
          </div>
        </div>
      </div>
      <div v-for="asset in filteredAssets" :key="asset.id"
        class="flex min-w-0 overflow-hidden pl-1 pr-3 py-3 rounded-lg bg-muted border hover:border-primary/40 transition-colors">
        <!-- 圆形背景 + 居中 Icon -->
        <div
          class="w-12 h-10 mt-3 shrink-0 flex items-center justify-center rounded-full bg-zinc-700 text-zinc-300 font-medium text-lg">
          <!-- <img v-if="asset.logo" :src="asset.logo" alt="logo" class="w-full h-full object-cover rounded-full" /> -->
          <span class="flex justify-center items-center w-10 h-10">{{ asset.label.charAt(0).toUpperCase() }}</span>
        </div>

        <div class="flex min-w-0 flex-1 flex-col justify-between h-full ml-3">
          <!-- 第一行：资产名称和数量 -->
          <div class="flex min-w-0 items-start justify-between gap-3">
            <div class="min-w-0 flex-1">
              <div class="truncate font-medium text-zinc-400">
                {{ asset.protocol === 'rgb11' ? asset.label : asset.label.toLocaleUpperCase() }}
              </div>
              <div v-if="asset.protocol === 'rgb11' && asset.display_name && asset.display_name !== asset.ticker"
                class="truncate text-xs text-zinc-400" :title="asset.display_name">
                {{ asset.display_name }}
              </div>
              <div v-if="asset.protocol === 'rgb11'" class="break-all font-mono text-[10px] leading-4 text-zinc-500"
                :title="asset.contract_id || `rgb:${asset.ticker}`">
                {{ $t('rgb11Transfer.assetId') }}: {{ asset.contract_id || `rgb:${asset.ticker}` }}
              </div>
            </div>
            <div class="shrink-0 text-right text-sm font-semibold text-zinc-300">
              {{ formatAmount(asset) }}
            </div>
          </div>

          <!-- 第二行：操作按钮 -->
          <div class="flex justify-end gap-2 mt-2">
            <!-- Lightning 模式按钮 -->
            <template v-if="mode === 'lightning'">
              <Button v-if="asset.protocol !== 'rgb11'" size="sm" variant="outline" @click="handleSend(asset)"
                class="text-zinc-400 border border-zinc-700/50 hover:bg-zinc-700 gap-[1px]">
                <Icon icon="lucide:send" class="w-4 h-4 mr-1" />
                {{ $t('l1AssetsTabs.send') }}
              </Button>
              <Button v-else size="sm" variant="outline" :disabled="rgb11State.consistency_status !== 'ok'" @click="handleSend(asset)"
                class="text-zinc-400 border border-zinc-700/50 hover:bg-zinc-700 gap-[1px]">
                <Icon icon="lucide:send" class="w-4 h-4 mr-1" />
                {{ $t('l1AssetsTabs.send') }}
              </Button>
              <Button v-if="asset.protocol === 'rgb11'" size="sm" variant="outline" @click="handleReceive(asset)"
                class="text-zinc-400 border border-zinc-700/50 hover:bg-zinc-700 gap-[1px]">
                <Icon icon="lucide:qr-code" class="w-4 h-4 mr-1" />
                {{ $t('l1AssetsTabs.receive') }}
              </Button>
              <Button v-if="asset.protocol !== 'rgb11'" size="sm" variant="outline" @click="handleSplicingIn(asset)"
                class="text-zinc-400 border border-zinc-700/50 hover:bg-zinc-700 gap-[1px]">
                <Icon icon="lets-icons:sign-in-squre" class="w-4 h-4 mr-1" />
                {{ $t('l1AssetsTabs.splicingIn') }}
              </Button>
            </template>
            <!-- Poolswap 模式按钮 -->
            <template v-else>
              <Button v-if="asset.protocol !== 'rgb11'" size="sm" variant="outline" @click="handleSend(asset)"
                class="text-zinc-400 border border-zinc-700/50 hover:bg-zinc-700 gap-[1px]">
                <Icon icon="lucide:send" class="w-4 h-4 mr-1" />
                {{ $t('l1AssetsTabs.send') }}
              </Button>
              <Button v-else size="sm" variant="outline" :disabled="rgb11State.consistency_status !== 'ok'" @click="handleSend(asset)"
                class="text-zinc-400 border border-zinc-700/50 hover:bg-zinc-700 gap-[1px]">
                <Icon icon="lucide:send" class="w-4 h-4 mr-1" />
                {{ $t('l1AssetsTabs.send') }}
              </Button>
              <Button v-if="asset.protocol === 'rgb11'" size="sm" variant="outline" @click="handleReceive(asset)"
                class="text-zinc-400 border border-zinc-700/50 hover:bg-zinc-700 gap-[1px]">
                <Icon icon="lucide:qr-code" class="w-4 h-4 mr-1" />
                {{ $t('l1AssetsTabs.receive') }}
              </Button>
              <Button v-if="asset.protocol !== 'rgb11'" size="sm" variant="outline" @click="handleDeposit(asset)"
                class="text-zinc-400 border border-zinc-700/50 hover:bg-zinc-700 gap-[1px]">
                <Icon icon="lucide:arrow-down-right" class="w-4 h-4 mr-1" />
                {{ $t('l1AssetsTabs.deposit') }}
              </Button>
            </template>
          </div>
          <div v-if="asset.protocol === 'rgb11'" class="mt-2 space-y-1 text-[11px] text-zinc-500">
            <div v-for="proof in rgb11Proofs(asset)" :key="`${proof.outpoint}:${proof.operation_id}`"
              class="min-w-0 rounded border border-zinc-700/60 p-2">
              <div class="break-all">Carrier: {{ proof.outpoint }}</div>
              <div>Method: {{ proof.carrier_binding?.commitment_method || 'unknown' }}</div>
              <div>Confirmations: {{ proof.confirmations || 0 }}</div>
              <div :class="proof.policy_status === 'rejected' ? 'text-red-400' : ''">
                Policy: {{ proof.policy_status || 'unchecked' }}
              </div>
              <div v-if="proof.policy_reason" class="break-all">Policy reason: {{ proof.policy_reason }}</div>
              <div class="break-all">Consignment: {{ proof.consignment_hash || 'local-only' }}</div>
              <div>UTXO lock: reason=rgb</div>
            </div>
          </div>
        </div>
      </div>
      <div v-if="selectedType === 'RGB11' && !filteredAssets.length"
        class="rounded-lg border border-dashed border-zinc-700 px-3 py-8 text-center text-xs text-zinc-500">
        {{ $t('rgb11Transfer.noAssets') }}
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Icon } from '@iconify/vue'
import { useRGB11Store, useWalletStore } from '@/store'
import { storeToRefs } from 'pinia'
import { Chain } from '@/types/index'
import { generateMempoolUrl, formatLargeNumber } from '@/utils'
import { useGlobalStore } from '@/store/global'
import walletManager from '@/utils/sat20'
import { useI18n } from 'vue-i18n'
// 类型定义
interface Asset {
  id: string
  ticker: string
  label: string
  amount: number | string
  precision?: number
  type?: string
  protocol?: string
  contract_id?: string
  display_name?: string
  symbol?: string
}

// Props定义
const props = defineProps<{
  modelValue?: string,
  assets: Asset[],
  mode: 'poolswap' | 'lightning'
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
  (e: 'splicing_in', asset: any): void
  (e: 'send', asset: any): void
  (e: 'receive', asset: any): void
  (e: 'deposit', asset: any): void
  (e: 'refresh'): void
  (e: 'issue-rgb11'): void
  (e: 'import-rgb11'): void
}>()

const walletStore = useWalletStore()
const rgb11Store = useRGB11Store()
const globalStore = useGlobalStore()
const { address, network } = storeToRefs(walletStore)
const { env, hideBalance } = storeToRefs(globalStore)
const { state: rgb11State } = storeToRefs(rgb11Store)
const { t } = useI18n()

const mempoolUrl = computed(() => {
  return generateMempoolUrl({
    network: network.value,
    path: `address/${address.value}`,
  })
})

// 资产类型
//const assetTypes = ['BTC', 'ORDX', 'Runes', 'BRC20']
const assetTypes = ['ORDX', 'Runes', 'BRC20', 'RGB11']
const selectedType = ref(props.modelValue || assetTypes[0])

// 过滤资产
const filteredAssets = computed(() => {
  // console.log('L1AssetsTabs - Received Assets:', props.assets)
  // console.log('L1AssetsTabs - Selected Type:', selectedType.value)

  return props.assets.filter(asset => {
    if (!asset) return false
    return true
    // if (selectedType.value === 'BTC' && !asset.type) {
    //   console.log('L1AssetsTabs - Found BTC asset:', asset)
    //   return true
    // }
    // const assetType = asset.type?.toUpperCase()
    // console.log('L1AssetsTabs - Asset:', asset, 'Type:', assetType, 'Selected:', selectedType.value)
    // return selectedType.value === assetType
  })
})

// 事件处理函数
const handleSend = (asset: any) => {
  // console.log('L1AssetsTabs - Send:', asset)
  emit('send', asset)
}

const handleReceive = (asset: any) => {
  emit('receive', asset)
}

const handleSplicingIn = (asset: any) => {
  // console.log('L1AssetsTabs - Splicing In:', asset)
  emit('splicing_in', asset)
}

const handleDeposit = (asset: any) => {
  // console.log('L1AssetsTabs - Deposit:', asset)
  emit('deposit', asset)
}

const rgb11Proofs = (asset: Asset) => (rgb11State.value.proofs || []).filter((proof: any) => (
  proof?.asset_name?.Protocol === 'rgb11' && proof?.asset_name?.Ticker === asset.ticker
))

const rgb11Transfers = computed(() => (
  [...(rgb11State.value.transfers || [])].reverse().slice(0, 8)
))

const rgb11TransferStatusClass = (status: string) => {
  if (status === 'settled') return 'text-emerald-400'
  if (status === 'conflicted' || status === 'failed') return 'text-red-400'
  return 'text-amber-400'
}

const syncing = ref(false)
const syncMessage = ref('')
const syncError = ref(false)

const backupRGB11State = async () => {
  if (!rgb11State.value.auto_backup_enabled && !window.confirm(t('rgb11Transfer.enableAutoBackupConfirm'))) return
  syncing.value = true
  syncMessage.value = ''
  const [err] = await walletManager.backupRGB11WalletState()
  syncing.value = false
  syncError.value = !!err
  syncMessage.value = err?.message || t('rgb11Transfer.backupDone')
  if (!err) emit('refresh')
}

const restoreRGB11State = async () => {
  syncing.value = true
  syncMessage.value = ''
  const [err] = await walletManager.restoreRGB11WalletState({ now: Date.now() })
  syncing.value = false
  syncError.value = !!err
  syncMessage.value = err?.message || t('rgb11Transfer.restoreDone')
  if (!err) emit('refresh')
}

// 监听资产类型变化
watch(selectedType, (newType) => {
  // console.log('L1AssetsTabs - Selected Type Changed:', newType)
  emit('update:modelValue', newType)
})

// 格式化金额显示
const formatExactAmount = (amount: number | string) => {
  const text = String(amount)
  const [integer, fraction] = text.split('.', 2)
  const sign = integer.startsWith('-') ? '-' : ''
  const digits = sign ? integer.slice(1) : integer
  const grouped = digits.replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  return fraction === undefined ? `${sign}${grouped}` : `${sign}${grouped}.${fraction}`
}

const formatAmount = (asset: Asset) => {
  if (hideBalance.value) {
    return '••••••'
  }
  if (asset.protocol === 'rgb11') {
    return formatExactAmount(asset.amount)
  }
  if (selectedType.value === 'BTC') {
    return `${Number(asset.amount)} sats`
  }
  return `${formatLargeNumber(Number(asset.amount))}`
}

const handlerRefresh = () => {
  console.log('L1AssetsTabs - Refresh')
  emit('refresh')
}
</script>

<style scoped>
.router-link-active {
  text-decoration: none;
}
</style>
