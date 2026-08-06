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
          <span>{{ $t('rgb11Transfer.accountManaged') }}</span>
        </div>
        <div v-if="rgb11State.consistency_status !== 'ok'" class="mt-1 text-amber-500">
          {{ $t('rgb11Transfer.inconsistentWarning') }}
        </div>
        <div v-if="rgb11Error" class="mt-1 break-all text-red-400">
          {{ $t('rgb11Transfer.stateError', { error: rgb11Error }) }}
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
        <div v-if="rgb11PendingTasks.length" class="mt-3 border-t border-zinc-700/70 pt-3">
          <div class="mb-2 flex items-center justify-between">
            <span class="font-medium text-amber-300">{{ $t('rgb11Transfer.pendingTasks') }}</span>
            <span>{{ rgb11PendingTasks.length }}</span>
          </div>
          <div class="space-y-2">
            <div v-for="task in rgb11PendingTasks" :key="task.key"
              class="rounded border border-amber-800/60 bg-amber-950/20 p-2">
              <div class="flex items-center justify-between gap-2">
                <span>{{ task.representative.asset?.Name?.Ticker || 'RGB11' }} · {{ rgb11TaskMode(task) }}</span>
                <span :class="rgb11TransferStatusClass(task.representative.status)">
                  {{ task.representative.status }}
                </span>
              </div>
              <div>{{ $t('rgb11Transfer.pendingTaskCount', { count: task.members.length }) }}</div>
              <div class="break-all">TX: {{ task.representative.witness_txid || task.representative.transfer_id }}</div>
              <div class="mt-2 grid grid-cols-2 gap-2">
                <Button size="sm" variant="outline" :disabled="rgb11TaskBusy === task.key"
                  @click="resumeRGB11Task(task)">
                  {{ rgb11TaskBusy === task.key ? $t('common.processing') : rgb11TaskActionLabel(task) }}
                </Button>
                <Button size="sm" variant="outline" :disabled="rgb11TaskBusy === task.key"
                  @click="refreshRGB11Task(task)">
                  {{ $t('rgb11Transfer.refreshTask') }}
                </Button>
                <Button v-if="canCancelRGB11Task(task)" size="sm" variant="outline"
                  class="col-span-2 text-red-400" :disabled="rgb11TaskBusy === task.key"
                  @click="cancelRGB11Task(task)">
                  {{ $t('rgb11Transfer.cancelTask') }}
                </Button>
              </div>
              <div v-if="rgb11TaskMessages[task.key]" class="mt-2 break-all"
                :class="rgb11TaskMessages[task.key].success ? 'text-emerald-400' : 'text-red-400'">
                {{ rgb11TaskMessages[task.key].text }}
              </div>
            </div>
          </div>
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
                :title="asset.canonical_name || `rgb11:${asset.type || 'f'}:${asset.ticker}`">
                {{ $t('rgb11Transfer.assetId') }}: {{ asset.canonical_name || `rgb11:${asset.type || 'f'}:${asset.ticker}` }}
              </div>
              <div v-if="asset.protocol === 'rgb11' && asset.contract_id" class="break-all font-mono text-[10px] leading-4 text-zinc-500"
                :title="asset.contract_id">
                {{ $t('rgb11Transfer.contractId') }}: {{ asset.contract_id }}
              </div>
              <div v-if="asset.protocol === 'rgb11'" class="text-[10px] leading-4 text-zinc-500">
                {{ $t('rgb11Transfer.fingerprint') }}: {{ asset.fingerprint || $t('rgb11Transfer.legacyFingerprint') }} ·
                {{ $t('rgb11Transfer.certification') }}: {{ $t(asset.verified ? 'rgb11Transfer.verified' : 'rgb11Transfer.unverified') }}
              </div>
            </div>
            <div class="shrink-0 text-right text-sm font-semibold text-zinc-300">
              {{ formatAmount(asset) }}
              <div v-if="asset.protocol === 'rgb11'" class="mt-1 text-[10px] font-normal text-zinc-500">
                {{ $t('rgb11Transfer.availableAmount') }}: {{ formatExactAmount(asset.available_amount || '0') }}
              </div>
              <div v-if="asset.protocol === 'rgb11' && isPositiveAmount(asset.pending_amount)"
                class="text-[10px] font-normal text-amber-400">
                {{ $t('rgb11Transfer.pendingAmount') }}: {{ formatExactAmount(asset.pending_amount || '0') }}
              </div>
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
              <Button v-else size="sm" variant="outline"
                :disabled="rgb11State.consistency_status !== 'ok' || !!rgb11Error || !hasAvailableRGB11(asset)"
                @click="handleSend(asset)"
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
              <Button v-else size="sm" variant="outline"
                :disabled="rgb11State.consistency_status !== 'ok' || !!rgb11Error || !hasAvailableRGB11(asset)"
                @click="handleSend(asset)"
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
              <div>UTXO lock: reason={{ rgb11ProofLockReason(proof) }}</div>
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
import rgb11Address from '@/utils/rgb11Address'
import { useI18n } from 'vue-i18n'
// 类型定义
interface Asset {
  id: string
  key?: string
  ticker: string
  label: string
  amount: number | string
  precision?: number
  type?: string
  protocol?: string
  canonical_name?: string
  contract_id?: string
  display_name?: string
  symbol?: string
  fingerprint?: string
  verified?: boolean
  available_amount?: string
  pending_amount?: string
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
const { state: rgb11State, error: rgb11Error } = storeToRefs(rgb11Store)
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

const rgb11AssetIdentity = (value: any) => {
  const name = value?.asset_name || value?.Name || value?.name || value
  const protocol = String(name?.Protocol || name?.protocol || '')
  const type = String(name?.Type || name?.type || '')
  const ticker = String(name?.Ticker || name?.ticker || '')
  return protocol && type && ticker ? `${protocol}:${type}:${ticker}` : ''
}

const rgb11Proofs = (asset: Asset) => {
  const expected = String(asset.canonical_name || asset.key || asset.id || '')
  if (!expected.startsWith('rgb11:')) return []
  return (rgb11State.value.proofs || []).filter((proof: any) => (
    rgb11AssetIdentity(proof) === expected && proof?.status !== 'spending'
  ))
}

type RGB11Task = {
  key: string
  members: any[]
  representative: any
}

type RGB11TaskMessage = { success: boolean; text: string }

const terminalRGB11Statuses = new Set(['settled', 'rejected', 'conflicted', 'failed', 'cancelled'])
const rgb11TaskBusy = ref('')
const rgb11TaskMessages = ref<Record<string, RGB11TaskMessage>>({})

const rgb11PendingTasks = computed<RGB11Task[]>(() => {
  const groups = new Map<string, any[]>()
  for (const transfer of rgb11State.value.transfers || []) {
    if (String(transfer?.direction || '').toLowerCase() !== 'send') continue
    if (terminalRGB11Statuses.has(String(transfer?.status || '').toLowerCase())) continue
    const key = String(transfer?.batch_id || transfer?.witness_txid || transfer?.transfer_id || '')
    if (!key) continue
    const values = groups.get(key) || []
    values.push(transfer)
    groups.set(key, values)
  }
  return [...groups.entries()].map(([key, members]) => ({
    key,
    members: [...members].sort((left, right) => String(left.transfer_id).localeCompare(String(right.transfer_id))),
    representative: members[0],
  })).reverse()
})

const rgb11Transfers = computed(() => (
  [...(rgb11State.value.transfers || [])].reverse().slice(0, 8)
))

const rgb11TransferStatusClass = (status: string) => {
  if (status === 'settled') return 'text-emerald-400'
  if (status === 'conflicted' || status === 'failed') return 'text-red-400'
  return 'text-amber-400'
}


const rgb11TaskTransport = (task: RGB11Task) => String(task.representative?.transport_mode || 'sat20-dkvs')
const rgb11TaskIsBroadcast = (task: RGB11Task) => task.members.every((item) => (
  ['broadcast', 'pending', 'settled'].includes(String(item?.status || '').toLowerCase())
))

const rgb11TaskMode = (task: RGB11Task) => {
  if (task.representative?.address_mode) return t('rgb11Transfer.addressMode')
  const transport = rgb11TaskTransport(task)
  if (transport === 'rgb-json-rpc') return 'RGB JSON-RPC'
  if (transport === 'out-of-band') return 'out-of-band'
  return 'SAT20 DKVS'
}

const rgb11TaskActionLabel = (task: RGB11Task) => {
  if (rgb11TaskIsBroadcast(task)) return t('rgb11Transfer.refreshTask')
  if (rgb11TaskTransport(task) === 'out-of-band') return t('rgb11Transfer.confirmOutOfBandBroadcast')
  if (rgb11TaskTransport(task) === 'sat20-dkvs') return t('rgb11Transfer.continueTask')
  return t('rgb11Transfer.retryTask')
}

const canCancelRGB11Task = (task: RGB11Task) => (
  rgb11TaskTransport(task) === 'out-of-band' && !rgb11TaskIsBroadcast(task)
)

const setRGB11TaskMessage = (task: RGB11Task, success: boolean, text: string) => {
  rgb11TaskMessages.value = {
    ...rgb11TaskMessages.value,
    [task.key]: { success, text },
  }
}

const reloadRGB11TaskState = async () => {
  const [stateErr, stateResult] = await walletManager.getRGB11State()
  if (stateErr || !stateResult?.state) {
    throw stateErr || new Error(t('rgb11Transfer.taskRefreshFailed'))
  }
  rgb11Store.setState(JSON.parse(stateResult.state))
  emit('refresh')
}

const refreshRGB11TaskState = async () => {
  const [refreshErr] = await walletManager.refreshRGB11State()
  if (refreshErr) throw refreshErr
  await reloadRGB11TaskState()
}

const completeRGB11TaskBroadcast = async (task: RGB11Task, txid: string) => {
  try {
    await refreshRGB11TaskState()
    setRGB11TaskMessage(task, true, t('rgb11Transfer.taskBroadcasted', { txid }))
  } catch (error: any) {
    setRGB11TaskMessage(task, true, t('rgb11Transfer.taskBroadcastedRefreshFailed', {
      txid,
      error: error?.message || t('rgb11Transfer.taskRefreshFailed'),
    }))
  }
}

const resumeManagedRGB11Task = async (task: RGB11Task) => {
  const transferIds: string[] = []
  const relayRecords: any[] = []
  const acks: any[] = []
  for (const member of task.members) {
    const transferId = String(member.transfer_id || '')
    if (!transferId) throw new Error(t('rgb11Transfer.taskResumeFailed'))
    const [recordErr, recordResult] = await walletManager.publishRGB11RelayRecord(transferId)
    if (recordErr || !recordResult?.record) {
      throw recordErr || new Error(t('rgb11Transfer.taskResumeFailed'))
    }
    const relayRecord = JSON.parse(recordResult.record)
    const [ackErr, ackResult] = await walletManager.fetchRGB11AckRecord(transferId)
    if (ackErr || !ackResult?.ack) {
      throw new Error(t('rgb11Transfer.taskWaitingAck'))
    }
    const ack = JSON.parse(ackResult.ack)
    if (ack?.accepted === false) {
      const [cancelErr] = await walletManager.cancelRGB11BatchByNack(
        transferId,
        JSON.stringify(relayRecord),
        JSON.stringify(ack),
      )
      if (cancelErr) throw cancelErr
      await reloadRGB11TaskState()
      setRGB11TaskMessage(task, true, t('rgb11Transfer.taskRejected', { reason: ack.reason_code || 'rejected' }))
      return
    }
    transferIds.push(transferId)
    relayRecords.push(relayRecord)
    acks.push(ack)
  }
  let broadcastErr: Error | undefined
  let broadcastResult: { txid: string } | undefined
  if (transferIds.length === 1) {
    ;[broadcastErr, broadcastResult] = await walletManager.broadcastRGB11Transfer(
      transferIds[0], JSON.stringify(relayRecords[0]), JSON.stringify(acks[0]),
    )
  } else {
    ;[broadcastErr, broadcastResult] = await walletManager.broadcastRGB11Batch({
      transfer_ids: transferIds,
      relay_records: relayRecords,
      acks,
    })
  }
  if (broadcastErr || !broadcastResult?.txid) {
    throw broadcastErr || new Error(t('rgb11Transfer.broadcastFailed'))
  }
  await completeRGB11TaskBroadcast(task, broadcastResult.txid)
}

const resumeRGB11Task = async (task: RGB11Task) => {
  if (rgb11TaskBusy.value) return
  rgb11TaskBusy.value = task.key
  setRGB11TaskMessage(task, true, '')
  try {
    if (rgb11TaskIsBroadcast(task)) {
      await refreshRGB11TaskState()
      setRGB11TaskMessage(task, true, t('rgb11Transfer.taskRefreshed'))
      return
    }
    const ids = task.members.map((item) => String(item.transfer_id || '')).filter(Boolean)
    if (!ids.length) throw new Error(t('rgb11Transfer.taskResumeFailed'))
    if (task.representative?.address_mode) {
      const [err, result] = await rgb11Address.deliverAndBroadcast({ transfer_id: ids[0] })
      if (err || !result?.txid) throw err || new Error(t('rgb11Transfer.broadcastFailed'))
      await completeRGB11TaskBroadcast(task, result.txid)
      return
    }
    const transport = rgb11TaskTransport(task)
    if (transport === 'rgb-json-rpc') {
      const [err, result] = await walletManager.deliverAndBroadcastRGB11ProxyTransfer(ids)
      if (err || !result?.txid) throw err || new Error(t('rgb11Transfer.broadcastFailed'))
      await completeRGB11TaskBroadcast(task, result.txid)
      return
    }
    if (transport === 'out-of-band') {
      if (!window.confirm(t('rgb11Transfer.outOfBandBroadcastConfirm'))) return
      const [err, result] = await walletManager.broadcastRGB11OutOfBand(ids)
      if (err || !result?.txid) throw err || new Error(t('rgb11Transfer.broadcastFailed'))
      await completeRGB11TaskBroadcast(task, result.txid)
      return
    }
    await resumeManagedRGB11Task(task)
  } catch (error: any) {
    setRGB11TaskMessage(task, false, error?.message || t('rgb11Transfer.taskResumeFailed'))
  } finally {
    rgb11TaskBusy.value = ''
  }
}

const refreshRGB11Task = async (task: RGB11Task) => {
  if (rgb11TaskBusy.value) return
  rgb11TaskBusy.value = task.key
  try {
    await refreshRGB11TaskState()
    setRGB11TaskMessage(task, true, t('rgb11Transfer.taskRefreshed'))
  } catch (error: any) {
    setRGB11TaskMessage(task, false, error?.message || t('rgb11Transfer.taskRefreshFailed'))
  } finally {
    rgb11TaskBusy.value = ''
  }
}

const cancelRGB11Task = async (task: RGB11Task) => {
  if (rgb11TaskBusy.value || !canCancelRGB11Task(task)) return
  if (!window.confirm(t('rgb11Transfer.taskCancelConfirm'))) return
  rgb11TaskBusy.value = task.key
  try {
    const [err] = await walletManager.cancelRGB11OutOfBandTransfer(String(task.representative.transfer_id || ''))
    if (err) throw err
    await reloadRGB11TaskState()
    setRGB11TaskMessage(task, true, t('rgb11Transfer.taskCancelled'))
  } catch (error: any) {
    setRGB11TaskMessage(task, false, error?.message || t('rgb11Transfer.taskCancelFailed'))
  } finally {
    rgb11TaskBusy.value = ''
  }
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

const isPositiveAmount = (amount: number | string | undefined) => {
  const value = String(amount || '0').trim()
  return value !== '' && !/^0*(?:\.0*)?$/.test(value)
}

const hasAvailableRGB11 = (asset: Asset) => isPositiveAmount(asset.available_amount)

const rgb11ProofLockReason = (proof: any) => (
  proof?.status === 'settled' && Number(proof?.confirmations || 0) > 0 ? 'rgb' : 'pending-rgb'
)

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
