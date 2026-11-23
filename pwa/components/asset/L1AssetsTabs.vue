<template>
  <div class="space-y-4">
    <!-- Asset Type Tabs -->
    <div class="flex justify-between border-b border-zinc-700 mb-4">
      <nav class="-mb-px flex min-w-0 flex-wrap gap-x-2 sm:gap-x-4">
        <button v-for="(type, index) in assetTypes" :key="index" @click="selectedType = type"
          :aria-pressed="selectedType === type"
          class="relative min-h-11 px-1 font-mono text-sm font-semibold" :class="{
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
        <Button size="icon" variant="ghost" class="size-11" :aria-label="$t('common.refresh')" @click="handlerRefresh">
          <Icon icon="lets-icons:refresh-2-light" aria-hidden="true" class="text-muted-foreground" />
        </Button>
        <Button v-if="mempoolUrl" size="icon" variant="ghost" as-child>
          <a :href="mempoolUrl" target="_blank" class="mb-[1px] hover:text-primary"
            :title="$t('l1AssetsTabs.viewTradeHistory')">
            <Icon icon="quill:link" class="text-zinc-400 hover:text-primary/90" />
          </a>
        </Button>
      </div>
    </div>

    <!-- Asset Lists -->
    <div class="space-y-2">
      <RGBVersion v-if="selectedType === 'RGB11'" />
      <div v-if="selectedType === 'RGB11'" class="rounded-lg border border-zinc-700 p-3 text-xs space-y-3">
        <p v-if="rgb11State.error_code || rgb11Error" class="text-red-400">{{ rgb11State.error_code || rgb11Error }}</p>
        <Button size="sm" variant="outline" @click="emit('issue-rgb11')">{{ $t('rgb11Transfer.issue') }}</Button>
        <Button size="sm" variant="outline" @click="emit('import-rgb11')">{{ $t('rgb11Transfer.import') }}</Button>
        <div v-for="task in rgb11State.transfers" :key="task.id" class="space-y-2 rounded border border-zinc-700 p-2">
          <p class="break-all font-mono">{{ task.id }}</p><p>{{ displayRGB11State(task.status) }}</p>
          <p v-for="(recipient, index) in task.recipients" :key="index" class="break-all">{{ recipient.amount.amount }} · {{ recipient.amount.asset }}</p>
          <p v-if="task.tx_id" class="break-all font-mono">{{ task.tx_id }}</p>
          <p v-if="task.error_code" class="text-red-400">{{ task.error_code }}</p>
          <Button v-if="task.can_confirm || task.can_cancel || task.can_export" size="sm" variant="outline" @click="resumeRGB11Task(task)">{{ $t('rgb11Transfer.retryTask') }}</Button>
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
              <div v-if="asset.protocol === 'rgb11' && asset.display_name && asset.display_name !== asset.label"
                class="truncate text-xs text-zinc-400" :title="asset.display_name">
                {{ asset.display_name }}
              </div>
              <div v-if="asset.protocol === 'rgb11'" class="break-all font-mono text-[10px] leading-4 text-zinc-500"
                :title="asset.canonical_name || asset.key || `rgb11:${asset.type || 'f'}:${asset.ticker}`">
                {{ $t('rgb11Transfer.assetId') }}: {{ asset.canonical_name || asset.key || `rgb11:${asset.type || 'f'}:${asset.ticker}` }}
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
          <div class="mt-2 flex flex-wrap justify-end gap-2">
            <!-- Lightning 模式按钮 -->
            <template v-if="mode === 'lightning'">
              <Button v-if="asset.protocol !== 'rgb11'" size="sm" variant="outline" @click="handleSend(asset)"
                class="text-zinc-400 border border-zinc-700/50 hover:bg-zinc-700 gap-[1px]">
                <Icon icon="lucide:send" class="w-4 h-4 mr-1" />
                {{ $t('l1AssetsTabs.send') }}
              </Button>
              <Button v-else size="sm" variant="outline"
                :disabled="!rgb11State.can_write || !!rgb11Error || !hasAvailableRGB11(asset)"
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
              <Button v-if="asset.protocol !== 'rgb11' || isFungibleRGB11(asset)" size="sm" variant="outline"
                :disabled="asset.protocol === 'rgb11' && !canMoveRGB11(asset)" @click="handleSplicingIn(asset)"
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
                :disabled="!rgb11State.can_write || !!rgb11Error || !hasAvailableRGB11(asset)"
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
              <Button v-if="asset.protocol !== 'rgb11' || isFungibleRGB11(asset)" size="sm" variant="outline"
                :disabled="asset.protocol === 'rgb11' && !canMoveRGB11(asset)" @click="handleDeposit(asset)"
                class="text-zinc-400 border border-zinc-700/50 hover:bg-zinc-700 gap-[1px]">
                <Icon icon="lucide:arrow-down-right" class="w-4 h-4 mr-1" />
                {{ $t('l1AssetsTabs.deposit') }}
              </Button>
            </template>
            <Button v-if="asset.protocol === 'rgb11' && asset.contract_id" size="sm" variant="outline"
              :disabled="!!rgb11Exporting" @click="exportRGB11Contract(asset, 'copy')"
              class="text-zinc-400 border border-zinc-700/50 hover:bg-zinc-700 gap-[1px]">
              <Icon icon="lucide:copy" class="w-4 h-4 mr-1" />
              {{ $t('rgb11Transfer.copyContractArmor') }}
            </Button>
            <Button v-if="asset.protocol === 'rgb11' && asset.contract_id" size="sm" variant="outline"
              :disabled="!!rgb11Exporting" @click="exportRGB11Contract(asset, 'save')"
              class="text-zinc-400 border border-zinc-700/50 hover:bg-zinc-700 gap-[1px]">
              <Icon icon="lucide:download" class="w-4 h-4 mr-1" />
              {{ $t('rgb11Transfer.downloadContract') }}
            </Button>
            <Button v-if="asset.protocol === 'rgb11' && asset.type === 'f' && hasIFAControl(asset)"
              size="sm" variant="outline" :disabled="!rgb11State.can_write || !!rgb11Error"
              @click="inflationAsset = asset; showInflation = true">
              {{ $t('rgb11Transfer.inflate') }}
            </Button>
          </div>
          <div v-if="asset.protocol === 'rgb11' && rgb11ExportMessages[asset.contract_id || '']"
            class="mt-1 break-words text-right text-[11px]"
            :role="rgb11ExportMessages[asset.contract_id || ''].success ? 'status' : 'alert'"
            :class="rgb11ExportMessages[asset.contract_id || ''].success ? 'text-emerald-400' : 'text-red-400'">
            {{ rgb11ExportMessages[asset.contract_id || ''].text }}
          </div>

        </div>
      </div>
      <div v-if="selectedType === 'RGB11' && !filteredAssets.length"
        class="rounded-lg border border-dashed border-zinc-700 px-3 py-8 text-center text-xs text-zinc-500">
        {{ $t('rgb11Transfer.noAssets') }}
      </div>
      <div v-if="selectedType !== 'RGB11' && !filteredAssets.length" data-testid="asset-empty-state"
        class="rounded-lg border border-dashed border-zinc-700 px-3 py-8 text-center text-xs text-zinc-500">
        {{ $t('l1AssetsTabs.noAssets', { type: $t(`l1AssetsTabs.assetType.${selectedType}`), network }) }}
      </div>
    </div>
    <RGB11SendDialog v-if="resumeTaskAsset" :key="resumeTaskId" v-model:open="showResumeTask"
      :asset="resumeTaskAsset" :resume-transfer-id="resumeTaskId" @completed="refreshRGB11TaskState" />
    <RGB11SendDialog v-if="inflationAsset" :key="inflationAsset.id" v-model:open="showInflation"
      :asset="inflationAsset" inflation @completed="refreshRGB11TaskState" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onUnmounted } from 'vue'
import RGB11SendDialog from '@/components/wallet/RGB11SendDialog.vue'
import RGBVersion from '@/components/asset/RGBVersion.vue'
import { Button } from '@/components/ui/button'
import { Icon } from '@iconify/vue'
import { useRGB11Store, useWalletStore } from '@/store'
import { storeToRefs } from 'pinia'
import { Chain } from '@/types/index'
import { generateMempoolUrl, formatLargeNumber } from '@/utils'
import { useGlobalStore } from '@/store/global'
import walletManager from '@/utils/sat20'
import { exportFileText } from '@/utils/accountManagement'
import { downloadRGB11ContractFile } from '@/utils/rgb11ContractFile'
import type { RGBTransferView } from '@/utils/walletViews'
import { walletRequestSessionGuard } from '@/lib/walletSession'
import { useI18n } from 'vue-i18n'
// 类型定义
interface Asset {
  id: string
  key?: string
  asset_key?: string
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
const { t, te } = useI18n()
const displayRGB11State = (value: unknown) => {
  const raw = String(value ?? '')
  const key = `walletComponents.states_${raw.replace(/-/g, '_')}`
  return te(key) ? t(key) : raw
}

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
watch(() => props.modelValue, (value) => { if (value) selectedType.value = value })

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

const showResumeTask = ref(false)
const resumeTaskId = ref('')
const resumeTaskAsset = ref<Asset | null>(null)
const showInflation = ref(false)
const inflationAsset = ref<Asset | null>(null)
const rgb11Exporting = ref('')
const rgb11ExportMessages = ref<Record<string, {success: boolean; text: string}>>({})
const rgbAsset = (asset: Asset) => rgb11State.value.assets.find(item => item.asset === (asset.key || asset.id))
const hasIFAControl = (asset: Asset) => rgbAsset(asset)?.can_inflate === true
const exportRGB11Contract = async (asset: Asset, action: 'copy' | 'save') => {
  const id = asset.contract_id || ''
  if (!id || rgb11Exporting.value) return
  rgb11Exporting.value = id
  delete rgb11ExportMessages.value[id]
  try {
    const checkSession = walletRequestSessionGuard('exportRGB11Contract')
    const readFile = async (extension: '.rgb' | '.asc') => {
      checkSession()
      const [error, files] = await walletManager.exportRGB11Contract(asset.key || asset.id)
      if (error) throw error
      const file = files?.find(file => file.name.endsWith(extension))
      if (!file?.data) throw new Error(t('rgb11Transfer.exportContractFailed'))
      return file
    }
    if (action === 'copy') {
      if (!navigator.clipboard?.write || typeof ClipboardItem === 'undefined') {
        throw new Error(t('rgb11Transfer.contractArmorCopyFailed'))
      }
      // Write from the click while the clipboard still has user activation.
      const armor = readFile('.asc').then(file => new Blob([exportFileText(file)], { type: 'text/plain' }))
      await Promise.all([navigator.clipboard.write([new ClipboardItem({ 'text/plain': armor })]), armor])
      rgb11ExportMessages.value[id] = { success: true, text: t('rgb11Transfer.contractArmorCopied') }
      return
    }
    const filename = downloadRGB11ContractFile(id, (await readFile('.rgb')).data)
    rgb11ExportMessages.value[id] = { success: true, text: t('common.fileDownloadStarted', { filename }) }
  } catch (error) {
    const text = action === 'copy' && error instanceof DOMException && error.name === 'NotAllowedError'
      ? t('rgb11Transfer.contractArmorCopyFailed')
      : error instanceof Error ? error.message : String(error)
    rgb11ExportMessages.value[id] = { success: false, text }
  } finally {
    rgb11Exporting.value = ''
  }
}
const resumeRGB11Task = (task: RGBTransferView) => {
  resumeTaskId.value = task.id
  resumeTaskAsset.value = props.assets.find(asset => (asset.key || asset.id) === task.recipients[0]?.amount.asset) || null
  showResumeTask.value = true
}
const refreshRGB11TaskState = async () => {
  const [error, value] = await walletManager.refreshRGB11State()
  if (error || !value) throw error || new Error(t('rgb11Transfer.taskRefreshFailed'))
  rgb11Store.setState(value); emit('refresh')
}
watch(selectedType, value => emit('update:modelValue', value), {immediate: true})
watch(() => [walletStore.walletId, walletStore.accountIndex, walletStore.network], () => {
  showResumeTask.value = false; resumeTaskAsset.value = null
  showInflation.value = false; inflationAsset.value = null
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

const hasAvailableRGB11 = (asset: Asset) => rgbAsset(asset)?.can_send === true
const isFungibleRGB11 = (asset: Asset) => asset.type === 'f' && /^[0-9a-f]{64}$/.test(asset.ticker)
const canMoveRGB11 = (asset: Asset) => isFungibleRGB11(asset) && rgb11State.value.can_write && hasAvailableRGB11(asset)

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

const handlerRefresh = async () => {
  if (selectedType.value === 'RGB11') {
    try { await refreshRGB11TaskState() }
    catch (error) { rgb11Store.setError(error instanceof Error ? error.message : String(error)) }
  } else emit('refresh')
}
</script>

<style scoped>
.router-link-active {
  text-decoration: none;
}
</style>
