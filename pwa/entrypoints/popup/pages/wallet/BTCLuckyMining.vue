<template>
  <LayoutHome>
    <WalletHeader />

    <div class="space-y-4 px-1 pb-4">
      <div class="flex items-center justify-between">
        <Button variant="ghost" size="sm" @click="router.back()">
          <Icon icon="lucide:arrow-left" class="h-4 w-4" />
        </Button>
        <div class="text-base font-medium">{{ $t('pwaUi.mining.title') }}</div>
        <Button variant="ghost" size="icon" :disabled="loading" @click="refreshPage">
          <Icon icon="lucide:refresh-cw" class="h-4 w-4" :class="{ 'animate-spin': loading }" />
        </Button>
      </div>

      <Card>
        <CardHeader class="space-y-1">
          <CardTitle class="text-base">{{ $t('pwaUi.mining.status') }}</CardTitle>
          <CardDescription>{{ status.running ? $t('pwaUi.mining.running') : $t('pwaUi.mining.stopped') }}</CardDescription>
        </CardHeader>
        <CardContent class="space-y-3 text-sm">
          <div class="grid grid-cols-2 gap-3">
            <div class="rounded-sm border border-border p-3">
              <div class="text-xs text-muted-foreground">{{ $t('pwaUi.mining.localHashrate') }}</div>
              <div class="mt-1 font-medium">{{ formatHashrate(status.hashesPerSecond) }}</div>
            </div>
            <div class="rounded-sm border border-border p-3">
              <div class="text-xs text-muted-foreground">{{ $t('pwaUi.mining.difficulty') }}</div>
              <div class="mt-1 font-medium">{{ formatDifficulty(currentDifficulty) }}</div>
              <div v-if="networkStats.currentDifficulty > 0" class="mt-1 text-[11px] text-muted-foreground">
                {{ $t('pwaUi.mining.networkDifficulty') }} {{ formatDifficulty(networkStats.currentDifficulty) }}
              </div>
            </div>
            <div class="rounded-sm border border-border p-3">
              <div class="text-xs text-muted-foreground">{{ $t('pwaUi.mining.networkHashrate') }}</div>
              <div class="mt-1 font-medium">{{ networkHashrateText }}</div>
              <div v-if="networkStats.source" class="mt-1 text-[11px] text-muted-foreground">
                {{ networkStats.source }}
              </div>
              <div v-if="networkStats.lastError" class="mt-1 truncate text-[11px] text-muted-foreground" :title="networkStats.lastError">
                {{ networkStats.lastError }}
              </div>
            </div>
            <div class="rounded-sm border border-border p-3">
              <div class="text-xs text-muted-foreground">{{ $t('pwaUi.mining.jobs') }}</div>
              <div class="mt-1 font-medium">{{ status.jobs || form.jobs }}</div>
            </div>
          </div>
          <div class="space-y-1">
            <div class="text-xs text-muted-foreground">{{ $t('pwaUi.mining.rewardAddress') }}</div>
            <div class="break-all rounded-sm bg-muted/70 p-2 text-xs">{{ status.rewardAddress || '-' }}</div>
          </div>
          <div class="rounded-sm border border-border p-3 text-xs">
            <div class="text-muted-foreground">{{ $t('pwaUi.mining.revenueSplit') }}</div>
            <div class="mt-2 grid grid-cols-3 gap-2 text-center">
              <div>
                <div class="font-medium">85%</div>
                <div class="mt-1 text-muted-foreground">{{ $t('pwaUi.mining.user') }}</div>
              </div>
              <div>
                <div class="font-medium">10%</div>
                <div class="mt-1 text-muted-foreground">{{ $t('pwaUi.mining.indexer') }}</div>
              </div>
              <div>
                <div class="font-medium">5%</div>
                <div class="mt-1 text-muted-foreground">{{ $t('pwaUi.mining.bootstrap') }}</div>
              </div>
            </div>
          </div>
          <div class="grid grid-cols-2 gap-3 text-xs">
            <div>
              <div class="text-muted-foreground">{{ $t('pwaUi.mining.btcHeight') }}</div>
              <div class="mt-1">{{ status.btcHeight || '-' }}</div>
            </div>
            <div>
              <div class="text-muted-foreground">{{ $t('pwaUi.mining.templateId') }}</div>
              <div class="mt-1 truncate" :title="status.templateId">{{ status.templateId || '-' }}</div>
            </div>
            <div>
              <div class="text-muted-foreground">{{ $t('pwaUi.mining.bestShare') }}</div>
              <div class="mt-1 truncate" :title="status.bestShare">{{ shortHash(status.bestShare) }}</div>
            </div>
            <div>
              <div class="text-muted-foreground">{{ $t('pwaUi.mining.target') }}</div>
              <div class="mt-1 truncate" :title="status.currentTarget">{{ shortHash(status.currentTarget) }}</div>
            </div>
            <div>
              <div class="text-muted-foreground">{{ $t('pwaUi.mining.lastTemplate') }}</div>
              <div class="mt-1">{{ formatTime(status.lastTemplateTime) }}</div>
            </div>
            <div>
              <div class="text-muted-foreground">{{ $t('pwaUi.mining.lastSubmit') }}</div>
              <div class="mt-1 truncate">{{ status.lastSubmitResult || '-' }}</div>
            </div>
          </div>
          <div v-if="status.lastError" class="rounded-sm border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive">
            {{ status.lastError }}
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle class="text-base">{{ $t('pwaUi.mining.configuration') }}</CardTitle>
        </CardHeader>
        <CardContent class="space-y-3">
          <div class="grid grid-cols-2 gap-3">
            <div class="space-y-1">
              <Label>{{ $t('pwaUi.mining.jobs') }}</Label>
              <Select v-model="form.jobs" :disabled="status.running">
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="1">1</SelectItem>
                  <SelectItem value="auto">{{ $t('pwaUi.mining.auto') }}</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div class="space-y-1">
              <Label>{{ $t('pwaUi.mining.sleep') }}</Label>
              <Input v-model="form.lowPrioritySleepMs" type="number" min="0" step="1" inputmode="numeric" :disabled="status.running" />
            </div>
          </div>
          <label class="flex items-center gap-3 rounded-sm border border-border p-3 text-sm">
            <input v-model="form.lowPriority" type="checkbox" class="h-4 w-4" :disabled="status.running" />
            <span>{{ $t('pwaUi.mining.lowPriority') }}</span>
          </label>
          <div class="grid grid-cols-2 gap-3">
            <Button :disabled="status.running || loading" @click="startMining">
              <Icon icon="lucide:play" class="h-4 w-4" />
              {{ $t('pwaUi.mining.start') }}
            </Button>
            <Button variant="secondary" :disabled="!status.running || loading" @click="stopMining">
              <Icon icon="lucide:square" class="h-4 w-4" />
              {{ $t('pwaUi.mining.stop') }}
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle class="text-base">{{ $t('pwaUi.mining.foundBlocks') }}</CardTitle>
        </CardHeader>
        <CardContent class="space-y-2">
          <div v-if="!status.foundBlocks?.length" class="text-sm text-muted-foreground">{{ $t('pwaUi.mining.noRecords') }}</div>
          <div
            v-for="block in status.foundBlocks"
            :key="block.blockHash"
            class="space-y-1 rounded-sm border border-border p-3 text-xs"
          >
            <div class="flex items-center justify-between gap-2">
              <span class="font-medium">{{ block.submitted ? $t('pwaUi.mining.submitted') : $t('pwaUi.mining.notSubmitted') }}</span>
              <span class="text-muted-foreground">{{ block.submitResult || '-' }}</span>
            </div>
            <div class="break-all text-muted-foreground">{{ block.blockHash }}</div>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle class="text-base">{{ $t('pwaUi.mining.claimRewards') }}</CardTitle>
          <CardDescription>{{ $t('pwaUi.mining.claimDescription') }}</CardDescription>
        </CardHeader>
        <CardContent class="space-y-3">
          <div v-if="rewardError" class="break-all text-xs text-destructive">{{ rewardError }}</div>
          <div v-if="claimResult" class="break-all text-xs">{{ $t('pwaUi.mining.claimTransaction') }}{{ claimResult }}</div>
          <div v-if="!rewards.length" class="text-sm text-muted-foreground">{{ $t('pwaUi.mining.noRewards') }}</div>
          <div v-for="reward in rewards" :key="reward.fundingUtxo" class="space-y-2 rounded-sm border border-border p-3 text-xs">
            <div class="break-all">{{ reward.fundingUtxo }}</div>
            <div v-if="reward.status !== 'error'" class="text-muted-foreground">{{ $t('pwaUi.mining.confirmations', { amount: reward.amount, confirmations: reward.confirmations, maturity: reward.maturity }) }}</div>
            <div v-if="reward.error" class="break-all text-destructive">{{ reward.error }}</div>
            <div v-if="reward.anchorTxId" class="break-all text-muted-foreground">{{ $t('pwaUi.mining.ascend') }}{{ reward.anchorTxId }}</div>
            <Button size="sm" :disabled="reward.status !== 'claimable' || !!claiming" @click="claimReward(reward.fundingUtxo)">
              {{ claiming === reward.fundingUtxo ? $t('pwaUi.mining.cosigning') : rewardState(reward.status) }}
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  </LayoutHome>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import { Icon } from '@iconify/vue'
import LayoutHome from '@/components/layout/LayoutHome.vue'
import WalletHeader from '@/components/wallet/HomeHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { useGlobalStore, useWalletStore } from '@/store'
import { Network } from '@/types'
import { getConfig } from '@/config/wasm'
import walletManager from '@/utils/sat20'

const { t } = useI18n()

const router = useRouter()
const walletStore = useWalletStore()
const globalStore = useGlobalStore()
const { network } = storeToRefs(walletStore)
const storageKey = 'btcLuckyMiningConfig'
const loading = ref(false)
const networkStatsLoading = ref(false)
const networkStatsFetchedAt = ref(0)
const rewards = ref<any[]>([])
const rewardError = ref('')
const claiming = ref('')
const claimResult = ref('')
const status = reactive<any>({
  enabled: false,
  running: false,
  jobs: 0,
  hashesPerSecond: 0,
  foundBlocks: [],
})
const form = reactive({
  jobs: '1',
  lowPriority: true,
  lowPrioritySleepMs: '100',
})
const networkStats = reactive({
  currentHashrate: 0,
  currentDifficulty: 0,
  source: '',
  lastError: '',
})
let timer: number | undefined

const difficultyOneTarget = BigInt('0x00000000ffff0000000000000000000000000000000000000000000000000000')

const saved = localStorage.getItem(storageKey)
if (saved) {
  try {
    const parsed = JSON.parse(saved)
    form.jobs = parsed.jobs === 'auto' ? 'auto' : '1'
    form.lowPriority = typeof parsed.lowPriority === 'boolean' ? parsed.lowPriority : true
    if (parsed.lowPrioritySleep && !parsed.lowPrioritySleepMs) {
      form.lowPrioritySleepMs = durationToMs(parsed.lowPrioritySleep)
    } else if (parsed.lowPrioritySleepMs) {
      form.lowPrioritySleepMs = durationToMs(String(parsed.lowPrioritySleepMs))
    }
  } catch {}
}

const applyStatus = (next: any) => {
  Object.assign(status, next || { enabled: false, running: false, foundBlocks: [] })
  status.foundBlocks = Array.isArray(next?.foundBlocks) ? next.foundBlocks : []
}

let readingStatus = false
let readingRewards = false
let forceRewards = false
let rewardHeights = ''
let rewardTimer: number | undefined

const refreshStatus = async () => {
  if (readingStatus) return
  readingStatus = true
  try {
    const [err, data] = await walletManager.getBTCLuckyMiningStatus()
    if (err) status.lastError = err.message
    else applyStatus(data)
  } finally {
    readingStatus = false
  }
}

const refreshRewards = async (force = false) => {
  if (readingRewards) {
    forceRewards ||= force
    return
  }
  readingRewards = true
  try {
    if (!force) {
      const cfg = getConfig(globalStore.env, network.value)
      const heights = await Promise.all([cfg.IndexerL1, cfg.IndexerL2].map(async endpoint => {
        const proxy = endpoint.Proxy.replace(/^\/|\/$/g, '')
        const url = `${endpoint.Scheme}://${endpoint.Host}/${proxy}/bestheight`
        const tip = await fetchJSON(url)
        if (tip.code !== 0 || !Number.isInteger(tip.data?.height)) throw new Error(tip.msg || 'Invalid indexer height')
        return tip.data.height
      }))
      const key = [status.rewardAddress, ...heights].join('/')
      if (key === rewardHeights && !rewardError.value) return
      rewardHeights = key
    }
    const [err, data] = await walletManager.getBTCLuckyRewards()
    rewardError.value = err?.message || ''
    if (!err) rewards.value = Array.isArray(data) ? data : []
  } catch (error: any) {
    rewardError.value = error?.message || String(error)
  } finally {
    readingRewards = false
    if (forceRewards) {
      forceRewards = false
      void refreshRewards(true)
    }
  }
}

const refreshPage = () => {
  void refreshStatus()
  void refreshRewards(true)
  void refreshNetworkStats()
}

const rewardState = (state: string) => ({
  unascended: t('pwaUi.mining.awaitAscend'), immature: t('pwaUi.mining.immature'), claimable: t('pwaUi.mining.claim'), spent: t('pwaUi.mining.spent'), error: t('pwaUi.mining.sourceError'),
}[state] || state)

const claimReward = async (utxo: string) => {
  if (claiming.value) return
  claiming.value = utxo
  claimResult.value = ''
  const [err, txid] = await walletManager.claimBTCLuckyReward(utxo)
  claiming.value = ''
  if (err) rewardError.value = err.message
  else {
    claimResult.value = typeof txid === 'string' ? txid : String(txid?.txid || txid)
    await refreshRewards(true)
  }
}

const startMining = async () => {
  loading.value = true
  localStorage.setItem(storageKey, JSON.stringify(form))
  const [err, data] = await walletManager.startBTCLuckyMining({
    jobs: form.jobs,
    lowPriority: form.lowPriority,
    lowPrioritySleep: `${normalizedSleepMs.value}ms`,
  })
  loading.value = false
  if (err) status.lastError = err.message
  else applyStatus(data)
}

const stopMining = async () => {
  loading.value = true
  const [err, data] = await walletManager.stopBTCLuckyMining()
  loading.value = false
  if (err) status.lastError = err.message
  else applyStatus(data)
}

const formatHashrate = (value?: number) => {
  const n = Number(value || 0)
  if (n >= 1_000_000_000_000_000_000_000) return `${(n / 1_000_000_000_000_000_000_000).toFixed(2)} ZH/s`
  if (n >= 1_000_000_000_000_000_000) return `${(n / 1_000_000_000_000_000_000).toFixed(2)} EH/s`
  if (n >= 1_000_000_000_000_000) return `${(n / 1_000_000_000_000_000).toFixed(2)} PH/s`
  if (n >= 1_000_000_000_000) return `${(n / 1_000_000_000_000).toFixed(2)} TH/s`
  if (n >= 1_000_000_000) return `${(n / 1_000_000_000).toFixed(2)} GH/s`
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)} MH/s`
  if (n >= 1_000) return `${(n / 1_000).toFixed(2)} KH/s`
  return `${n.toFixed(0)} H/s`
}

const formatHashrateWithExponent = (value?: number) => {
  const n = Number(value || 0)
  if (!Number.isFinite(n) || n <= 0) return '-'
  return `${formatHashrate(n)} (${n.toExponential(2)} H/s)`
}

const normalizedSleepMs = computed(() => {
  const n = Number(form.lowPrioritySleepMs)
  return Number.isFinite(n) && n >= 0 ? Math.floor(n) : 100
})

const networkHashrateText = computed(() => {
  const value = Number(networkStats.currentHashrate || status.networkHashesPerSecond || status.networkHashrate || 0)
  if (value > 0) return formatHashrateWithExponent(value)
  return networkStats.currentDifficulty > 0 ? formatHashrateWithExponent((networkStats.currentDifficulty * 4_294_967_296) / 600) : '-'
})

const currentDifficulty = computed(() => difficultyFromTarget(status.currentTarget))

async function refreshNetworkStats(force = false) {
  const now = Date.now()
  if (!force && now - networkStatsFetchedAt.value < 120_000) return
  if (networkStatsLoading.value) return
  networkStatsLoading.value = true
  try {
    const data = await fetchNetworkStats()
    networkStats.currentHashrate = data.hashrate
    networkStats.currentDifficulty = data.difficulty
    networkStats.source = data.source
    networkStats.lastError = ''
    networkStatsFetchedAt.value = now
  } catch (error: any) {
    networkStats.lastError = error?.message || String(error)
    networkStatsFetchedAt.value = now
  } finally {
    networkStatsLoading.value = false
  }
}

async function fetchNetworkStats() {
  if (network.value === Network.REGTEST) {
    throw new Error(t('pwaUi.mining.regtestStatsUnavailable'))
  }
  const errors: string[] = []
  const mempoolURL = network.value === Network.MAINNET
    ? 'https://mempool.space/api/v1/mining/hashrate/3d'
    : 'https://mempool.space/testnet4/api/v1/mining/hashrate/3d'
  try {
    const data = await fetchJSON(mempoolURL)
    const hashrate = Number(data?.currentHashrate || latestHashrate(data?.hashrates) || 0)
    const difficulty = Number(data?.currentDifficulty || latestDifficulty(data?.difficulty) || 0)
    if (hashrate > 0 || difficulty > 0) return { hashrate, difficulty, source: 'mempool.space' }
    errors.push(t('pwaUi.mining.mempoolStatsEmpty'))
  } catch (error: any) {
    errors.push(`mempool.space: ${error?.message || String(error)}`)
  }

  if (network.value !== Network.MAINNET) {
    throw new Error(errors.join('; '))
  }

  try {
    const data = await fetchJSON('https://api.blockchair.com/bitcoin/stats')
    const hashrate = Number(data?.data?.hashrate_24h || 0)
    const difficulty = Number(data?.data?.difficulty || 0)
    if (hashrate > 0 || difficulty > 0) return { hashrate, difficulty, source: 'blockchair.com' }
    errors.push(t('pwaUi.mining.blockchairStatsEmpty'))
  } catch (error: any) {
    errors.push(`blockchair.com: ${error?.message || String(error)}`)
  }

  try {
    const data = await fetchJSON('https://api.blockchain.info/charts/hash-rate?timespan=1days&format=json')
    const last = Array.isArray(data?.values) ? data.values[data.values.length - 1] : undefined
    const hashrate = Number(last?.y || 0) * 1_000_000_000_000
    if (hashrate > 0) return { hashrate, difficulty: 0, source: 'blockchain.info' }
    errors.push(t('pwaUi.mining.blockchainStatsEmpty'))
  } catch (error: any) {
    errors.push(`blockchain.info: ${error?.message || String(error)}`)
  }

  throw new Error(errors.join('; '))
}

async function fetchJSON(url: string) {
  const resp = await fetch(url, { cache: 'no-store', signal: AbortSignal.timeout(10_000) })
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
  return resp.json()
}

function latestHashrate(items: any[] = []) {
  for (let i = items.length - 1; i >= 0; i -= 1) {
    const value = Number(items[i]?.avgHashrate || 0)
    if (value > 0) return value
  }
  return 0
}

function latestDifficulty(items: any[] = []) {
  for (let i = items.length - 1; i >= 0; i -= 1) {
    const value = Number(items[i]?.difficulty || 0)
    if (value > 0) return value
  }
  return 0
}

function difficultyFromTarget(targetHex?: string) {
  const hex = String(targetHex || '').replace(/^0x/i, '').trim()
  if (!hex) return 0
  try {
    const target = BigInt(`0x${hex}`)
    if (target <= 0n) return 0
    return Number(difficultyOneTarget) / Number(target)
  } catch {
    return 0
  }
}

function formatDifficulty(value: number) {
  if (!Number.isFinite(value) || value <= 0) return '-'
  if (value >= 1_000_000_000_000) return `${(value / 1_000_000_000_000).toFixed(2)} T`
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(2)} B`
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(2)} M`
  if (value >= 1_000) return `${(value / 1_000).toFixed(2)} K`
  if (value < 0.0001) return value.toExponential(2)
  return value.toFixed(value >= 10 ? 2 : 4)
}

function shortHash(value?: string) {
  const text = String(value || '')
  if (!text) return '-'
  return text.length > 20 ? `${text.slice(0, 10)}...${text.slice(-8)}` : text
}

function formatTime(value?: string) {
  if (!value) return '-'
  const t = new Date(value)
  if (Number.isNaN(t.getTime())) return '-'
  return t.toLocaleTimeString()
}

function durationToMs(value: string) {
  const text = String(value || '').trim()
  const match = text.match(/^([0-9]+(?:\.[0-9]+)?)(ms|s|m)?$/i)
  if (!match) return '100'
  const amount = Number(match[1])
  if (!Number.isFinite(amount) || amount < 0) return '100'
  const unit = (match[2] || 'ms').toLowerCase()
  if (unit === 'm') return String(Math.round(amount * 60_000))
  if (unit === 's') return String(Math.round(amount * 1_000))
  return String(Math.round(amount))
}

onMounted(() => {
  refreshPage()
  timer = window.setInterval(() => {
    if (status.running) void refreshStatus()
    void refreshNetworkStats()
  }, 2000)
  rewardTimer = window.setInterval(() => void refreshRewards(), 15000)
})

onUnmounted(() => {
  window.clearInterval(timer)
  window.clearInterval(rewardTimer)
})
</script>
