<template>
  <section class="space-y-4" data-testid="mining-dashboard">
    <div class="overflow-hidden rounded-xl border border-emerald-400/20 bg-zinc-950 p-4 text-zinc-100 shadow-sm">
      <div class="flex items-center justify-between gap-2 text-xs">
        <span class="flex items-center gap-2 font-medium text-emerald-300">
          <span class="h-2 w-2 rounded-full" :class="status.running ? 'bg-emerald-400 motion-safe:animate-pulse' : 'bg-zinc-500'" />
          {{ status.running ? t('pwaUi.mining.live') : t('pwaUi.mining.stopped') }}
        </span>
        <span class="text-zinc-400">{{ t('pwaUi.mining.activeJobs', { count: status.jobs || 0 }) }}</span>
      </div>
      <div class="mt-4 text-xs text-zinc-400">{{ t('pwaUi.mining.localHashrate') }}</div>
      <div class="mt-1 font-mono text-3xl font-semibold tracking-tight" data-testid="mining-live-rate">
        {{ status.running && !fresh ? '—' : formatRate(status.running ? status.hashesPerSecond : 0) }}
      </div>
      <div class="mt-3 grid grid-cols-2 gap-3 text-xs">
        <div>
          <div class="text-zinc-400">{{ t('pwaUi.mining.average30s') }}</div>
          <div class="mt-1 font-mono" data-testid="mining-average">{{ average === null ? '—' : formatRate(average) }}</div>
        </div>
        <div>
          <div class="text-zinc-400">{{ t('pwaUi.mining.observedPeak') }}</div>
          <div class="mt-1 font-mono">{{ peak === null ? '—' : formatRate(peak) }}</div>
        </div>
      </div>
      <svg viewBox="0 0 600 144" preserveAspectRatio="none" class="mt-4 h-28 w-full" role="img" :aria-label="t('pwaUi.mining.chartLabel')" data-testid="mining-chart">
        <defs>
          <linearGradient :id="gradientId" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stop-color="#34d399" stop-opacity="0.25" />
            <stop offset="100%" stop-color="#34d399" stop-opacity="0" />
          </linearGradient>
        </defs>
        <path v-for="y in [16, 72, 128]" :key="y" :d="'M0 ' + y + ' H600'" stroke="#3f3f46" stroke-dasharray="3 6" fill="none" />
        <g v-for="(segment, index) in chart" :key="index">
          <path :d="segment.area" :fill="'url(#' + gradientId + ')'" />
          <path :d="segment.line" stroke="#34d399" stroke-width="2" vector-effect="non-scaling-stroke" fill="none" data-testid="mining-chart-line" />
        </g>
      </svg>
      <div class="flex justify-between text-[10px] text-zinc-500">
        <span>{{ t('pwaUi.mining.twoMinutesAgo') }}</span>
        <span>{{ t('pwaUi.mining.now') }}</span>
      </div>
      <div class="mt-3 text-xs text-zinc-400" data-testid="mining-sampling-note">
        {{ cachedTemplate ? t('pwaUi.mining.cachedTemplate') : status.running && !fresh ? t('pwaUi.mining.waitingSample') : t('pwaUi.mining.samplingNote') }}
      </div>
    </div>

    <slot name="controls" />

    <div class="rounded-xl border border-border bg-card p-4">
      <div class="flex items-center justify-between gap-2">
        <h2 class="text-sm font-medium">{{ t('pwaUi.mining.personalChallenge') }}</h2>
        <span class="text-[10px] text-muted-foreground">{{ t('pwaUi.mining.sinceStart') }}</span>
      </div>
      <div class="mt-4 flex items-baseline gap-2">
        <span class="font-mono text-4xl font-semibold text-emerald-600 dark:text-emerald-400" data-testid="mining-best-zeros">{{ bestZeros ?? '—' }}</span>
        <span class="text-xs text-muted-foreground">{{ t('pwaUi.mining.zeroBits') }}</span>
      </div>
      <div v-if="bestZeros !== null && targetZeros !== null" class="mt-2 text-xs text-muted-foreground" data-testid="mining-zero-gap">
        {{ t('pwaUi.mining.zeroGap', { target: targetZeros, gap: Math.max(0, targetZeros - bestZeros) }) }}
      </div>
      <div v-if="bestZeros !== null && targetZeros !== null" class="mt-3 h-1.5 overflow-hidden rounded-full bg-muted" role="meter" :aria-label="t('pwaUi.mining.zeroComparison')" aria-valuemin="0" :aria-valuemax="Math.max(1, targetZeros)" :aria-valuenow="Math.min(bestZeros, Math.max(1, targetZeros))">
        <div class="h-full rounded-full bg-emerald-500 transition-[width] motion-reduce:transition-none" :style="{ width: Math.min(100, bestZeros / Math.max(1, targetZeros) * 100) + '%' }" />
      </div>
      <div class="mt-4 grid grid-cols-4 gap-2">
        <div v-for="bits in milestones" :key="bits" class="rounded-lg border p-2 text-center" :class="bestZeros !== null && bestZeros >= bits ? 'border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300' : 'border-border text-muted-foreground'" :data-reached="bestZeros !== null && bestZeros >= bits" :data-testid="'mining-milestone-' + bits">
          <Icon :icon="bestZeros !== null && bestZeros >= bits ? 'lucide:circle-check' : 'lucide:circle-dashed'" class="mx-auto h-4 w-4" />
          <div class="mt-1 font-mono text-sm">{{ bits }}</div>
          <div class="text-[10px]">{{ t('pwaUi.mining.bits') }}</div>
          <span class="sr-only">{{ bestZeros !== null && bestZeros >= bits ? t('pwaUi.mining.reached') : t('pwaUi.mining.notReached') }}</span>
        </div>
      </div>
      <div class="mt-3 min-h-8 text-xs" aria-live="polite" aria-atomic="true" data-testid="mining-record-message">
        <span v-if="recordMessage" class="font-medium text-emerald-600 dark:text-emerald-400">{{ recordMessage }}</span>
        <span v-else class="text-muted-foreground">{{ bestZeros === null ? t('pwaUi.mining.challengeEmpty') : t('pwaUi.mining.challengeHint') }}</span>
      </div>
      <details v-if="bestHash" class="mt-1 text-xs">
        <summary class="cursor-pointer text-muted-foreground">{{ t('pwaUi.mining.inspectBest') }}</summary>
        <div class="mt-2 break-all rounded-md bg-muted/70 p-2 font-mono leading-relaxed" data-testid="mining-full-hash"><span class="font-semibold text-emerald-600 dark:text-emerald-400">{{ bestHash.slice(0, hexZeroCount) }}</span>{{ bestHash.slice(hexZeroCount) }}</div>
      </details>
      <p class="mt-3 text-[11px] leading-relaxed text-muted-foreground">{{ t('pwaUi.mining.zeroDisclaimer') }}</p>
    </div>

    <div class="rounded-xl border border-border bg-card p-4" data-testid="mining-network-comparison">
      <div class="flex items-center gap-2 text-sm font-medium">
        <Icon icon="lucide:globe-2" class="h-4 w-4 text-muted-foreground" />
        {{ t('pwaUi.mining.networkComparison') }}
      </div>
      <div class="mt-3 font-mono text-xl">{{ networkRate > 0 ? formatRate(networkRate) : '—' }}</div>
      <div v-if="comparison" class="mt-2 text-sm" data-testid="mining-network-ratio">{{ t('pwaUi.mining.networkRatio', { count: comparison }) }}</div>
      <p v-else class="mt-2 text-xs text-muted-foreground" data-testid="mining-network-unavailable">{{ networkNote }}</p>
      <div v-if="networkStats.source && networkStats.updatedAt" class="mt-3 flex flex-wrap justify-between gap-1 text-[10px] text-muted-foreground">
        <span>{{ networkStats.source }}</span>
        <span>{{ t('pwaUi.mining.networkUpdated', { time: new Date(networkStats.updatedAt).toLocaleTimeString(locale) }) }}</span>
      </div>
      <p class="mt-2 text-[11px] text-muted-foreground">{{ t('pwaUi.mining.networkEstimate') }}</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@iconify/vue'

interface DisplayStatus {
  running?: boolean
  jobs?: number
  hashesPerSecond?: number
  bestShare?: string
  currentTarget?: string
  templateId?: string
  lastError?: string
}

interface NetworkStats {
  currentHashrate: number
  currentDifficulty: number
  source: string
  lastError: string
  updatedAt: number
}

interface RateSample {
  at: number
  rate: number | null
}

const props = defineProps<{
  status: DisplayStatus
  sampleAt: number
  networkStats: NetworkStats
  networkKey: string
  templateDifficulty: number
}>()
const { t, locale } = useI18n()
const gradientId = 'mining-' + useId()
const milestones = [20, 24, 28, 32]
const samples = ref<RateSample[]>([])
const now = ref(Date.now())
const record = ref<{ bits: number, milestone: boolean } | null>(null)
const recordMessage = computed(() => record.value
  ? t('pwaUi.mining.' + (record.value.milestone ? 'milestoneReached' : 'recordImproved'), { bits: record.value.bits })
  : '')
let timer: ReturnType<typeof setInterval> | undefined
let recordTimer: ReturnType<typeof setTimeout> | undefined
let initialized = false
let wasRunning = false
let previousBest = ''

function normalizedHash(value?: string) {
  const hex = String(value || '').replace(/^0x/i, '').toLowerCase()
  return /^[0-9a-f]{64}$/.test(hex) ? hex : ''
}

function zeroBits(hex: string): number | null {
  if (!hex) return null
  const n = BigInt('0x' + hex)
  return n === 0n ? 256 : 256 - n.toString(2).length
}

const bestHash = computed(() => normalizedHash(props.status.bestShare))
const bestZeros = computed(() => zeroBits(bestHash.value))
const targetZeros = computed(() => {
  const hex = normalizedHash(props.status.currentTarget)
  return hex && BigInt('0x' + hex) > 0n ? zeroBits(hex) : null
})
const hexZeroCount = computed(() => bestHash.value.match(/^0*/)?.[0].length || 0)
const fresh = computed(() => props.sampleAt > 0 && now.value - props.sampleAt < 6000)
const cachedTemplate = computed(() => props.status.running && props.status.templateId && props.status.lastError)
const windowSamples = computed(() => samples.value.filter(s => s.at >= now.value - 120_000))
const recentRates = computed(() => windowSamples.value.filter(s => s.at >= now.value - 30_000 && s.rate !== null).map(s => s.rate!))
const average = computed(() => recentRates.value.length ? recentRates.value.reduce((a, b) => a + b, 0) / recentRates.value.length : null)
const peak = computed(() => {
  const rates = windowSamples.value.filter(s => s.rate !== null).map(s => s.rate!)
  return rates.length ? Math.max(...rates) : null
})

const chart = computed(() => {
  const result: { line: string, area: string }[] = []
  const scale = Math.max(1, peak.value || 0) * 1.12
  let points: number[][] = []
  let lastAt = 0
  const finish = () => {
    if (!points.length) return
    const line = points.map(([x, y], i) => (i ? 'L' : 'M') + x.toFixed(2) + ' ' + y.toFixed(2)).join(' ')
    result.push({ line, area: line + ' L' + points.at(-1)![0].toFixed(2) + ' 144 L' + points[0][0].toFixed(2) + ' 144 Z' })
    points = []
  }
  for (const sample of windowSamples.value) {
    if (sample.rate === null || sample.at - lastAt > 6000) finish()
    if (sample.rate !== null) points.push([Math.max(0, Math.min(600, (sample.at - now.value + 120_000) / 200)), 128 - Math.min(scale, sample.rate) / scale * 112])
    lastAt = sample.at
  }
  finish()
  return result
})

const networkRate = computed(() => props.networkKey === 'regtest' ? 0 : Math.max(0, Number(props.networkStats.currentHashrate) || 0))
const networkFresh = computed(() => props.networkStats.updatedAt > 0 && now.value - props.networkStats.updatedAt < 300_000 && !props.networkStats.lastError)
const matchingDifficulty = computed(() => props.templateDifficulty > 0 && props.networkStats.currentDifficulty > 0 && Math.abs(props.templateDifficulty / props.networkStats.currentDifficulty - 1) < 0.000001)
const comparison = computed(() => {
  const local = Number(props.status.hashesPerSecond)
  if (!props.status.running || !fresh.value || !networkFresh.value || !matchingDifficulty.value || !networkRate.value || !(local > 0)) return ''
  const ratio = networkRate.value / local
  if (ratio < 1) return ''
  return new Intl.NumberFormat(locale.value, { notation: 'compact', maximumSignificantDigits: 3 }).format(ratio)
})
const networkNote = computed(() => {
  if (props.networkKey === 'regtest') return t('pwaUi.mining.regtestStatsUnavailable')
  if (!networkFresh.value) return t('pwaUi.mining.networkNotReady')
  if (!matchingDifficulty.value) return t('pwaUi.mining.networkMismatch')
  return t('pwaUi.mining.networkStartHint')
})

function formatRate(value?: number) {
  const rate = Number.isFinite(Number(value)) ? Math.max(0, Number(value) || 0) : 0
  const units = ['H/s', 'kH/s', 'MH/s', 'GH/s', 'TH/s', 'PH/s', 'EH/s', 'ZH/s']
  const index = rate > 0 ? Math.min(units.length - 1, Math.floor(Math.log10(rate) / 3)) : 0
  return (rate / 1000 ** Math.max(0, index)).toFixed(index > 0 ? 2 : 0) + ' ' + units[Math.max(0, index)]
}

function resetDisplay() {
  samples.value = []
  record.value = null
  previousBest = ''
  initialized = false
  wasRunning = false
  clearTimeout(recordTimer)
}

function recordSample() {
  now.value = Date.now()
  if (!props.sampleAt) return
  if (initialized && props.status.running && !wasRunning) resetDisplay()
  const hex = bestHash.value
  if (initialized && props.status.running && hex && (!previousBest || hex < previousBest)) {
    const oldZeros = zeroBits(previousBest) || 0
    const crossed = milestones.filter(bits => bits > oldZeros && bits <= (bestZeros.value || 0))
    record.value = { bits: crossed.at(-1) ?? bestZeros.value ?? 0, milestone: crossed.length > 0 }
    clearTimeout(recordTimer)
    recordTimer = setTimeout(() => { record.value = null }, 8000)
  }
  previousBest = hex
  wasRunning = Boolean(props.status.running)
  initialized = true
  const rate = Number(props.status.hashesPerSecond)
  samples.value = [...samples.value.filter(s => s.at >= now.value - 120_000 && s.at !== props.sampleAt), {
    at: props.sampleAt,
    rate: props.status.running && !document.hidden && Number.isFinite(rate) && rate >= 0 ? rate : null,
  }].slice(-80)
}

function onVisibility() {
  now.value = Date.now()
  samples.value = [...samples.value, { at: now.value, rate: null }].slice(-80)
}

watch(() => props.networkKey, resetDisplay)
watch(() => props.sampleAt, recordSample, { immediate: true })
onMounted(() => {
  timer = setInterval(() => { now.value = Date.now() }, 2000)
  document.addEventListener('visibilitychange', onVisibility)
})
onUnmounted(() => {
  clearInterval(timer)
  clearTimeout(recordTimer)
  document.removeEventListener('visibilitychange', onVisibility)
})
</script>
