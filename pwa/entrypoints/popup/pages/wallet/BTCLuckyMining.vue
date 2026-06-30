<template>
  <LayoutHome>
    <WalletHeader />

    <div class="space-y-4 px-1 pb-4">
      <div class="flex items-center justify-between">
        <Button variant="ghost" size="sm" @click="router.back()">
          <Icon icon="lucide:arrow-left" class="h-4 w-4" />
        </Button>
        <div class="text-base font-medium">BTC Lucky Mining</div>
        <Button variant="ghost" size="icon" :disabled="loading" @click="refreshStatus">
          <Icon icon="lucide:refresh-cw" class="h-4 w-4" :class="{ 'animate-spin': loading }" />
        </Button>
      </div>

      <Card>
        <CardHeader class="space-y-1">
          <CardTitle class="text-base">挖矿状态</CardTitle>
          <CardDescription>{{ status.running ? '正在参与抽奖' : '未运行' }}</CardDescription>
        </CardHeader>
        <CardContent class="space-y-3 text-sm">
          <div class="grid grid-cols-2 gap-3">
            <div class="rounded-sm border border-border p-3">
              <div class="text-xs text-muted-foreground">本机算力</div>
              <div class="mt-1 font-medium">{{ formatHashrate(status.hashesPerSecond) }}</div>
            </div>
            <div class="rounded-sm border border-border p-3">
              <div class="text-xs text-muted-foreground">当前难度</div>
              <div class="mt-1 font-medium">{{ formatDifficulty(currentDifficulty) }}</div>
            </div>
            <div class="rounded-sm border border-border p-3">
              <div class="text-xs text-muted-foreground">全网算力</div>
              <div class="mt-1 font-medium">{{ networkHashrateText }}</div>
              <div v-if="networkStats.source" class="mt-1 text-[11px] text-muted-foreground">
                {{ networkStats.source }}
              </div>
              <div v-if="networkStats.lastError" class="mt-1 truncate text-[11px] text-muted-foreground" :title="networkStats.lastError">
                {{ networkStats.lastError }}
              </div>
            </div>
            <div class="rounded-sm border border-border p-3">
              <div class="text-xs text-muted-foreground">Jobs</div>
              <div class="mt-1 font-medium">{{ status.jobs || form.jobs }}</div>
            </div>
          </div>
          <div class="space-y-1">
            <div class="text-xs text-muted-foreground">Reward Address（钱包通道地址）</div>
            <div class="break-all rounded-sm bg-muted/70 p-2 text-xs">{{ status.rewardAddress || '-' }}</div>
          </div>
          <div class="rounded-sm border border-border p-3 text-xs">
            <div class="text-muted-foreground">挖矿收益分成</div>
            <div class="mt-2 grid grid-cols-3 gap-2 text-center">
              <div>
                <div class="font-medium">85%</div>
                <div class="mt-1 text-muted-foreground">用户</div>
              </div>
              <div>
                <div class="font-medium">10%</div>
                <div class="mt-1 text-muted-foreground">服务节点</div>
              </div>
              <div>
                <div class="font-medium">5%</div>
                <div class="mt-1 text-muted-foreground">引导节点</div>
              </div>
            </div>
          </div>
          <div class="grid grid-cols-2 gap-3 text-xs">
            <div>
              <div class="text-muted-foreground">BTC Height</div>
              <div class="mt-1">{{ status.btcHeight || '-' }}</div>
            </div>
            <div>
              <div class="text-muted-foreground">Job ID</div>
              <div class="mt-1 truncate" :title="status.jobId">{{ status.jobId || '-' }}</div>
            </div>
            <div>
              <div class="text-muted-foreground">Best Share</div>
              <div class="mt-1 truncate" :title="status.bestShare">{{ shortHash(status.bestShare) }}</div>
            </div>
            <div>
              <div class="text-muted-foreground">Target</div>
              <div class="mt-1 truncate" :title="status.currentTarget">{{ shortHash(status.currentTarget) }}</div>
            </div>
            <div>
              <div class="text-muted-foreground">Last Job</div>
              <div class="mt-1">{{ formatTime(status.lastJobTime) }}</div>
            </div>
            <div>
              <div class="text-muted-foreground">Last Submit</div>
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
          <CardTitle class="text-base">配置</CardTitle>
        </CardHeader>
        <CardContent class="space-y-3">
          <div class="grid grid-cols-2 gap-3">
            <div class="space-y-1">
              <Label>Jobs</Label>
              <Select v-model="form.jobs" :disabled="status.running">
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="1">1</SelectItem>
                  <SelectItem value="auto">auto</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div class="space-y-1">
              <Label>Sleep (ms)</Label>
              <Input v-model="form.lowPrioritySleepMs" type="number" min="0" step="1" inputmode="numeric" :disabled="status.running" />
            </div>
          </div>
          <label class="flex items-center gap-3 rounded-sm border border-border p-3 text-sm">
            <input v-model="form.lowPriority" type="checkbox" class="h-4 w-4" :disabled="status.running" />
            <span>低优先级</span>
          </label>
          <div class="grid grid-cols-2 gap-3">
            <Button :disabled="status.running || loading" @click="startMining">
              <Icon icon="lucide:play" class="h-4 w-4" />
              启动
            </Button>
            <Button variant="secondary" :disabled="!status.running || loading" @click="stopMining">
              <Icon icon="lucide:square" class="h-4 w-4" />
              停止
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle class="text-base">中奖记录</CardTitle>
        </CardHeader>
        <CardContent class="space-y-2">
          <div v-if="!status.foundBlocks?.length" class="text-sm text-muted-foreground">暂无记录</div>
          <div
            v-for="block in status.foundBlocks"
            :key="`${block.jobId}-${block.blockHash}`"
            class="space-y-1 rounded-sm border border-border p-3 text-xs"
          >
            <div class="flex items-center justify-between gap-2">
              <span class="font-medium">{{ block.submitted ? '已提交' : '未提交' }}</span>
              <span class="text-muted-foreground">{{ block.submitResult || '-' }}</span>
            </div>
            <div class="break-all text-muted-foreground">{{ block.blockHash }}</div>
          </div>
        </CardContent>
      </Card>
    </div>
  </LayoutHome>
</template>

<script setup lang="ts">
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
import { useWalletStore } from '@/store'
import { Network } from '@/types'
import { getConfig } from '@/config/wasm'
import walletManager from '@/utils/sat20'

const router = useRouter()
const walletStore = useWalletStore()
const { network } = storeToRefs(walletStore)
const storageKey = 'btcLuckyMiningConfig'
const loading = ref(false)
const networkStatsLoading = ref(false)
const networkStatsFetchedAt = ref(0)
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
  if (!Array.isArray(status.foundBlocks)) status.foundBlocks = []
}

const refreshStatus = async () => {
  loading.value = true
  const [err, data] = await walletManager.getBTCLuckyMiningStatus()
  loading.value = false
  if (!err) applyStatus(data)
  if (!status.rewardAddress) refreshRewardAddress()
  refreshNetworkStats()
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
  if (!err) applyStatus(data)
}

const stopMining = async () => {
  loading.value = true
  const [err, data] = await walletManager.stopBTCLuckyMining()
  loading.value = false
  if (!err) applyStatus(data)
  if (!status.rewardAddress) refreshRewardAddress()
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
  return currentDifficulty.value > 0 ? formatHashrateWithExponent((currentDifficulty.value * 4_294_967_296) / 600) : '-'
})

const currentDifficulty = computed(() => {
  const publicDifficulty = Number(networkStats.currentDifficulty || 0)
  return publicDifficulty > 0 ? publicDifficulty : difficultyFromTarget(status.currentTarget)
})

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
  const errors: string[] = []
  const mempoolURL = network.value === Network.LIVENET
    ? 'https://mempool.space/api/v1/mining/hashrate/3d'
    : 'https://mempool.space/testnet4/api/v1/mining/hashrate/3d'
  try {
    const data = await fetchJSON(mempoolURL)
    const hashrate = Number(data?.currentHashrate || latestHashrate(data?.hashrates) || 0)
    const difficulty = Number(data?.currentDifficulty || latestDifficulty(data?.difficulty) || 0)
    if (hashrate > 0 || difficulty > 0) return { hashrate, difficulty, source: 'mempool.space' }
    errors.push('mempool.space returned empty stats')
  } catch (error: any) {
    errors.push(`mempool.space: ${error?.message || String(error)}`)
  }

  if (network.value !== Network.LIVENET) {
    throw new Error(errors.join('; '))
  }

  try {
    const data = await fetchJSON('https://api.blockchair.com/bitcoin/stats')
    const hashrate = Number(data?.data?.hashrate_24h || 0)
    const difficulty = Number(data?.data?.difficulty || 0)
    if (hashrate > 0 || difficulty > 0) return { hashrate, difficulty, source: 'blockchair.com' }
    errors.push('blockchair.com returned empty stats')
  } catch (error: any) {
    errors.push(`blockchair.com: ${error?.message || String(error)}`)
  }

  try {
    const data = await fetchJSON('https://api.blockchain.info/charts/hash-rate?timespan=1days&format=json')
    const last = Array.isArray(data?.values) ? data.values[data.values.length - 1] : undefined
    const hashrate = Number(last?.y || 0) * 1_000_000_000_000
    if (hashrate > 0) return { hashrate, difficulty: 0, source: 'blockchain.info' }
    errors.push('blockchain.info returned empty stats')
  } catch (error: any) {
    errors.push(`blockchain.info: ${error?.message || String(error)}`)
  }

  throw new Error(errors.join('; '))
}

async function fetchJSON(url: string) {
  const resp = await fetch(url, { cache: 'no-store' })
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

async function refreshRewardAddress() {
  if (status.rewardAddress) return
  try {
    const env = localStorage.getItem('env') || 'test'
    const cfg = getConfig(env, network.value || Network.TESTNET)
    const pubkey = serviceNodePubkey(cfg.Peers)
    if (!pubkey) throw new Error('indexer pubkey is empty')
    const [err, result] = await walletManager.getChannelAddrByPeerPubkey(pubkey)
    if (err) throw err
    if (result?.channelAddr) {
      status.rewardAddress = result.channelAddr
      if (!status.lastError) status.lastError = ''
    }
  } catch (error: any) {
    if (!status.lastError) status.lastError = error?.message || String(error)
  }
}

function serviceNodePubkey(peers: string[] = []) {
  const servicePeer = peers.find((peer) => peer.startsWith('s@')) || peers[0] || ''
  const parts = servicePeer.split('@')
  return String(parts[1] || '').trim()
}

function difficultyFromTarget(targetHex?: string) {
  const hex = String(targetHex || '').replace(/^0x/i, '').trim()
  if (!hex) return 0
  try {
    const target = BigInt(`0x${hex}`)
    if (target <= 0n) return 0
    const scaled = (difficultyOneTarget * 10000n) / target
    return Number(scaled) / 10000
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
  refreshStatus()
  refreshRewardAddress()
  refreshNetworkStats(true)
  timer = window.setInterval(refreshStatus, 2000)
})

onUnmounted(() => {
  if (timer) window.clearInterval(timer)
})
</script>
