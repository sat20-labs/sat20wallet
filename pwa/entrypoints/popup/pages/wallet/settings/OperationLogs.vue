<template>
  <LayoutHome>
    <WalletHeader />
    <div class="px-4 pb-4">
      <div class="flex items-center justify-between gap-3">
        <div>
          <h2 class="text-2xl font-medium text-zinc-600/90">{{ $t('pwaUi.logs.title') }}</h2>
          <p class="mt-1 text-xs text-muted-foreground">{{ $t('pwaUi.logs.description') }}</p>
        </div>
        <Button
          variant="destructive"
          size="sm"
          :disabled="deleting || logs.length === 0"
          @click="deleteAll"
        >
          <Icon :icon="deleting ? 'lucide:loader' : 'lucide:trash-2'" class="mr-1 h-4 w-4" :class="{ 'animate-spin': deleting }" />
          {{ $t('pwaUi.logs.deleteAll') }}
        </Button>
      </div>

      <div v-if="errorMessage" class="mt-4 rounded-md border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-500">
        {{ errorMessage }}
      </div>

      <div v-if="loading && logs.length === 0" class="flex justify-center py-12 text-muted-foreground">
        <Icon icon="lucide:loader" class="h-5 w-5 animate-spin" />
      </div>
      <div v-else-if="logs.length === 0" class="py-12 text-center text-sm text-muted-foreground">
        {{ $t('pwaUi.logs.empty') }}
      </div>

      <div v-else class="mt-4 space-y-2">
        <button
          v-for="item in logs"
          :key="item.id"
          type="button"
          class="w-full rounded-lg border border-border/70 bg-card/70 p-3 text-left transition hover:bg-accent/60"
          @click="openDetail(item.id)"
        >
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <span class="truncate text-sm font-medium">{{ item.title }}</span>
                <span class="rounded px-1.5 py-0.5 text-[10px] font-medium" :class="statusClass(item.status)">
                  {{ statusText(item.status) }}
                </span>
              </div>
              <p class="mt-1 line-clamp-2 text-xs text-muted-foreground">{{ item.summary }}</p>
              <div class="mt-2 flex items-center gap-2 text-[11px] text-muted-foreground/80">
                <span>{{ formatTime(item.updated_at) }}</span>
                <span v-if="item.txid" class="truncate">{{ shortValue(item.txid) }}</span>
              </div>
            </div>
            <Icon icon="lucide:chevron-right" class="mt-1 h-4 w-4 shrink-0 text-muted-foreground" />
          </div>
        </button>
      </div>
    </div>

    <div v-if="detailVisible" class="fixed inset-0 z-50 flex items-end bg-black/50 sm:items-center sm:justify-center" @click.self="closeDetail">
      <div class="max-h-[88vh] w-full overflow-y-auto rounded-t-2xl border border-border bg-background p-4 shadow-xl sm:max-w-lg sm:rounded-2xl">
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <h3 class="text-lg font-semibold">{{ detail?.title || $t('pwaUi.logs.detail') }}</h3>
            <p v-if="detail" class="mt-1 text-sm text-muted-foreground">{{ detail.summary }}</p>
          </div>
          <Button variant="ghost" size="icon" @click="closeDetail">
            <Icon icon="lucide:x" class="h-4 w-4" />
          </Button>
        </div>

        <div v-if="detailLoading" class="flex justify-center py-10">
          <Icon icon="lucide:loader" class="h-5 w-5 animate-spin" />
        </div>
        <template v-else-if="detail">
          <div class="mt-4 grid grid-cols-2 gap-2 text-xs">
            <div class="rounded-md bg-muted/50 p-2">
              <div class="text-muted-foreground">{{ $t('pwaUi.logs.status') }}</div>
              <div class="mt-1 font-medium">{{ statusText(detail.status) }}</div>
            </div>
            <div class="rounded-md bg-muted/50 p-2">
              <div class="text-muted-foreground">{{ $t('pwaUi.logs.updated') }}</div>
              <div class="mt-1 font-medium">{{ formatTime(detail.updated_at) }}</div>
            </div>
          </div>

          <section v-if="detail.action === 'rgb11_send_address'" class="mt-5" :aria-label="$t('pwaUi.logs.rgbStatus')">
            <div class="flex items-center justify-between gap-3">
              <h4 class="text-sm font-medium">{{ $t('pwaUi.logs.rgbStatus') }}</h4>
              <Button variant="outline" size="sm" :disabled="rgbStatusLoading" @click="readRGBTaskStatus">
                {{ rgbStatusLoading ? $t('pwaUi.logs.reading') : $t('pwaUi.logs.readCurrent') }}
              </Button>
            </div>
            <p class="mt-2 text-xs text-muted-foreground">{{ $t('pwaUi.logs.readOnlyHint') }}</p>
            <p v-if="rgbStatusError" role="status" class="mt-2 text-xs text-red-500">{{ rgbStatusError }}</p>
            <dl v-if="rgbStatus" class="mt-2 space-y-2 rounded-md border border-border/70 p-3 text-xs">
              <div v-for="([key, value]) in fieldEntries({
                transfer_id: rgbStatus.transfer_id,
                current_status: rgbStatus.status || $t('pwaUi.logs.notRecorded'),
                ACK: rgbStatus.ack_status || $t('pwaUi.logs.notRecorded'),
                delivery_record_key: rgbStatus.delivery_record_key || $t('pwaUi.logs.notRecorded'),
                delivery_record_hash: rgbStatus.delivery_record_hash || $t('pwaUi.logs.notRecorded'),
                delivery_acknowledged: rgbStatus.delivery_acknowledged === null ? $t('pwaUi.logs.notRecorded') : String(rgbStatus.delivery_acknowledged),
                witness_txid: rgbStatus.witness_txid || $t('pwaUi.logs.notRecorded'),
                rejection_reason: rgbStatus.reject_reason || $t('pwaUi.logs.notRecorded'),
                matching_logged_error: rgbStatus.logged_error || $t('pwaUi.logs.notRecorded'),
              })" :key="key" class="grid grid-cols-[7rem_1fr] gap-2">
                <dt class="text-muted-foreground">{{ formatKey(key) }}</dt>
                <dd class="break-all text-right">{{ value }}</dd>
              </div>
            </dl>
            <p v-if="rgbStatus" class="mt-2 text-xs text-muted-foreground">{{ $t('pwaUi.logs.historyHint') }}</p>
          </section>

          <section v-if="hasFields(detail.parameters)" class="mt-5">
            <h4 class="text-sm font-medium">{{ $t('pwaUi.logs.parameters') }}</h4>
            <dl class="mt-2 space-y-2 rounded-md border border-border/70 p-3 text-xs">
              <div v-for="([key, value]) in fieldEntries(detail.parameters)" :key="key" class="grid grid-cols-[7rem_1fr] gap-2">
                <dt class="text-muted-foreground">{{ formatKey(key) }}</dt>
                <dd class="break-all text-right">{{ value }}</dd>
              </div>
            </dl>
          </section>

          <section v-if="hasFields(detail.result)" class="mt-5">
            <h4 class="text-sm font-medium">{{ $t('pwaUi.logs.result') }}</h4>
            <dl class="mt-2 space-y-2 rounded-md border border-border/70 p-3 text-xs">
              <div v-for="([key, value]) in fieldEntries(detail.result)" :key="key" class="grid grid-cols-[7rem_1fr] gap-2">
                <dt class="text-muted-foreground">{{ formatKey(key) }}</dt>
                <dd class="break-all text-right">{{ value }}</dd>
              </div>
            </dl>
          </section>

          <section class="mt-5">
            <h4 class="text-sm font-medium">{{ $t('pwaUi.logs.history') }}</h4>
            <div class="mt-2 space-y-3">
              <div v-for="(event, index) in detail.history" :key="`${event.timestamp}-${index}`" class="relative border-l border-border pl-4">
                <span class="absolute -left-1 top-1 h-2 w-2 rounded-full bg-foreground/70" />
                <div class="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
                  <span>{{ statusText(event.status || detail.status) }}</span>
                  <span>{{ formatTime(event.timestamp) }}</span>
                </div>
                <p class="mt-1 text-sm">{{ event.message }}</p>
                <dl v-if="hasFields(event.details)" class="mt-2 space-y-1 text-xs">
                  <div v-for="([key, value]) in fieldEntries(event.details)" :key="key" class="grid grid-cols-[7rem_1fr] gap-2">
                    <dt class="text-muted-foreground">{{ formatKey(key) }}</dt>
                    <dd class="break-all text-right">{{ value }}</dd>
                  </div>
                </dl>
              </div>
            </div>
          </section>
        </template>
      </div>
    </div>
  </LayoutHome>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { Icon } from '@iconify/vue'
import LayoutHome from '@/components/layout/LayoutHome.vue'
import WalletHeader from '@/components/wallet/HomeHeader.vue'
import { Button } from '@/components/ui/button'
import walletManager from '@/utils/sat20'
import { useGlobalStore, useWalletStore } from '@/store'
import {
  deleteAllOperationLogs,
  getOperationLog,
  getOperationLogs,
  projectSettledRGB11OperationLogs,
  projectRGB11OperationStatus,
  type RGB11OperationStatus,
  type OperationLogRecord,
  type OperationLogStatus,
} from '@/utils/operationLog'

const { t, te, locale } = useI18n()

const logs = ref<OperationLogRecord[]>([])
const detail = ref<OperationLogRecord | null>(null)
const loading = ref(false)
const detailLoading = ref(false)
const deleting = ref(false)
const detailVisible = ref(false)
const errorMessage = ref('')
const rgbStatus = ref<RGB11OperationStatus | null>(null)
const rgbStatusLoading = ref(false)
const rgbStatusError = ref('')
let rgbStatusGeneration = 0
let refreshTimer: ReturnType<typeof setInterval> | undefined
const walletStore = useWalletStore()
const globalStore = useGlobalStore()
const currentScope = () => JSON.stringify([globalStore.env, walletStore.rootAccountId,
  walletStore.walletId, walletStore.accountIndex, walletStore.network, walletStore.address, walletStore.locked])
let alive = true
watch(currentScope, resetRGBTaskStatus, { flush: 'sync' })

async function loadLogs() {
  if (loading.value) return
  loading.value = true
  const scope = currentScope()
  const [error, items] = await getOperationLogs()
  if (!alive || scope !== currentScope()) { loading.value = false; return }
  if (error) {
    loading.value = false
    errorMessage.value = error.message
    return
  }
  let displayed = items
  if (items.some(item => item.action === 'rgb11_send_address' && ['pending', 'running'].includes(item.status))) {
    try {
      const [rgbError, rgbResult] = await walletManager.getRGB11State()
      if (!rgbError && rgbResult?.state) {
        const state = JSON.parse(rgbResult.state)
        if (Array.isArray(state.transfers)) displayed = projectSettledRGB11OperationLogs(items, state.transfers)
      }
    } catch { /* Keep stored logs when current RGB state cannot be read. */ }
  }
  loading.value = false
  if (!alive || scope !== currentScope()) return
  errorMessage.value = ''
  logs.value = displayed
  if (detailVisible.value && detail.value) {
    const latest = displayed.find(item => item.id === detail.value?.id)
    if (latest) detail.value = latest
  }
}

async function openDetail(id: string) {
  resetRGBTaskStatus()
  const scope = currentScope()
  detailVisible.value = true
  detailLoading.value = true
  detail.value = null
  const [error, item] = await getOperationLog(id)
  detailLoading.value = false
  if (!alive || scope !== currentScope()) return
  if (error) {
    errorMessage.value = error.message
    detailVisible.value = false
    return
  }
  detail.value = logs.value.find(record => record.id === id) || item
}

function closeDetail() {
  resetRGBTaskStatus()
  detailVisible.value = false
  detail.value = null
}

function resetRGBTaskStatus() {
  rgbStatusGeneration++
  rgbStatus.value = null
  rgbStatusError.value = ''
  rgbStatusLoading.value = false
}

async function readRGBTaskStatus() {
  const record = detail.value
  if (!record || record.action !== 'rgb11_send_address' || rgbStatusLoading.value) return
  resetRGBTaskStatus()
  const generation = rgbStatusGeneration
  const scope = currentScope()
  const isCurrent = () => alive && generation === rgbStatusGeneration && scope === currentScope() &&
    detailVisible.value && detail.value?.id === record.id
  rgbStatusLoading.value = true
  try {
    const [error, response] = await walletManager.getRGB11State()
    if (!isCurrent()) return
    if (error || !response?.state) throw new Error(t('pwaUi.logs.rgbUnavailable'))
    const state = JSON.parse(response.state)
    const snapshot = projectRGB11OperationStatus(record, Array.isArray(state.transfers) ? state.transfers : [], {
      walletId: walletStore.walletId, accountIndex: walletStore.accountIndex,
    })
    if (!snapshot) {
      rgbStatusError.value = t('pwaUi.logs.noUniqueTransfer')
      return
    }
    rgbStatus.value = snapshot
  } catch {
    if (isCurrent()) rgbStatusError.value = t('pwaUi.logs.readFailed')
  } finally {
    if (generation === rgbStatusGeneration) rgbStatusLoading.value = false
  }
}

async function deleteAll() {
  if (!window.confirm(t('pwaUi.logs.deleteConfirm'))) return
  deleting.value = true
  const error = await deleteAllOperationLogs()
  deleting.value = false
  if (error) {
    errorMessage.value = error.message
    return
  }
  logs.value = []
  closeDetail()
}

function formatTime(timestamp: number) {
  if (!timestamp) return '-'
  return new Date(timestamp).toLocaleString(locale.value === 'zh' ? 'zh-CN' : 'en-US')
}

function shortValue(value: string) {
  if (!value || value.length <= 18) return value
  return `${value.slice(0, 8)}…${value.slice(-8)}`
}

function statusText(status: string) {
  const key = `pwaUi.logs.states.${status}`
  return te(key) ? t(key) : status
}

function formatKey(key: string) {
  const messageKey = `pwaUi.logs.fields.${key}`
  return te(messageKey) ? t(messageKey) : key.replaceAll('_', ' ')
}

function hasFields(value?: Record<string, string>) {
  return !!value && Object.keys(value).length > 0
}

function fieldEntries(value?: Record<string, string>) {
  return Object.entries(value || {})
}

function statusClass(status: OperationLogStatus) {
  switch (status) {
    case 'succeeded': return 'bg-green-500/15 text-green-600'
    case 'failed': return 'bg-red-500/15 text-red-500'
    case 'cancelled': return 'bg-zinc-500/15 text-zinc-500'
    case 'pending': return 'bg-amber-500/15 text-amber-600'
    default: return 'bg-blue-500/15 text-blue-600'
  }
}

onMounted(() => {
  void loadLogs()
  refreshTimer = setInterval(() => void loadLogs(), 3000)
})

onUnmounted(() => {
  alive = false
  resetRGBTaskStatus()
  if (refreshTimer) clearInterval(refreshTimer)
})
</script>
