<template>
  <LayoutSecond :title="t('pwaUi.logs.title')">
    <div class="min-w-0 space-y-4 break-words">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <p class="text-sm text-muted-foreground">{{ t('pwaUi.logs.description') }}</p>
        <Button variant="outline" size="sm" :disabled="busy" @click="refresh">{{ t('common.refresh') }}</Button>
      </div>
      <p v-if="error" role="alert" class="break-words rounded-lg border border-destructive/50 p-3 text-sm text-destructive">{{ error }}</p>
      <p v-else-if="busy && !records.length" role="status" class="py-6 text-center text-sm text-muted-foreground">{{ t('loading') }}</p>
      <p v-else-if="!records.length" class="rounded-xl border border-dashed border-border p-6 text-center text-sm text-muted-foreground">{{ t('pwaUi.logs.empty') }}</p>
      <details v-for="{ record, result } in rows" :key="record.id" class="rounded-xl border border-border bg-card p-3"
        @toggle="toggleDetails(record, $event)">
        <summary class="cursor-pointer text-sm font-medium">
          {{ record.contract ? contractTitle(record.contract) : kindLabel(record.kind) }} · {{ operationStatus(record, record.status) }}
          <span class="mt-1 block text-xs font-normal text-muted-foreground">{{ new Date(record.updated_at).toLocaleString() }}</span>
        </summary>
        <div class="mt-3 space-y-2 border-t border-border pt-3 text-sm">
          <p v-for="(amount,index) in record.amounts" :key="index">{{ amount.amount }} {{ amount.asset === '::' ? 'sats' : amount.asset }}</p>
          <dl v-if="record.contract" class="space-y-3">
            <div v-if="record.contract.url">
              <dt class="text-xs text-muted-foreground">{{ t('pwaUi.logs.contractId') }}</dt>
              <dd class="break-all font-mono text-xs">{{ record.contract.url }}</dd>
            </div>
            <div>
              <dt class="text-xs text-muted-foreground">{{ t('pwaUi.logs.contractAction') }}</dt>
              <dd class="break-all font-mono text-xs">{{ record.contract.action }}</dd>
            </div>
            <div v-if="record.tx_ids[0]">
              <dt class="text-xs text-muted-foreground">{{ t('pwaUi.logs.invokeTx') }} · {{ chainLabel(record.contract.chain) }}</dt>
              <dd class="break-all font-mono text-xs">
                <a v-if="txLink(record.tx_ids[0], record.contract.chain)" :href="txLink(record.tx_ids[0], record.contract.chain)"
                  class="text-primary underline underline-offset-2" target="_blank" rel="noopener noreferrer">{{ record.tx_ids[0] }}</a>
                <span v-else>{{ record.tx_ids[0] }}</span>
              </dd>
            </div>
            <div v-if="record.tx_ids[0]">
              <dt class="text-xs text-muted-foreground">
                {{ t('pwaUi.logs.resultTx') }}<span v-if="result?.status === 'ready'"> · {{ chainLabel(result.chain) }}</span>
              </dt>
              <dd v-if="result?.status === 'ready'" class="break-all font-mono text-xs">
                <a v-if="txLink(result.txid, result.chain)" :href="txLink(result.txid, result.chain)"
                  class="text-primary underline underline-offset-2" target="_blank" rel="noopener noreferrer">{{ result.txid }}</a>
                <span v-else>{{ result.txid }}</span>
              </dd>
              <dd v-else class="text-sm">{{ t(`pwaUi.logs.contractResult.${result?.status || 'loading'}`) }}</dd>
            </div>
          </dl>
          <template v-else>
            <p v-for="tx in record.tx_ids" :key="tx" class="break-all font-mono text-xs">{{ tx }}</p>
          </template>
          <p v-if="record.error_code" class="break-all text-destructive">{{ record.error_code }}</p>
          <ol class="space-y-1 text-xs text-muted-foreground">
            <li v-for="(event,index) in record.history" :key="index">{{ new Date(event.time).toLocaleString() }} · {{ operationStatus(record, event.status) }} <span v-if="event.error_code">{{ event.error_code }}</span></li>
          </ol>
        </div>
      </details>
      <Button v-if="records.length" variant="ghost" class="text-destructive hover:text-destructive" :disabled="busy" @click="confirmClear = true">{{ t('pwaUi.logs.deleteAll') }}</Button>
    </div>
    <AlertDialog v-model:open="confirmClear">
    <AlertDialogContent class="w-[calc(100%_-_2rem)] max-w-md rounded-xl">
        <AlertDialogHeader>
          <AlertDialogTitle>{{ t('pwaUi.logs.deleteAll') }}</AlertDialogTitle>
          <AlertDialogDescription>{{ t('pwaUi.logs.deleteConfirm') }}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{{ t('common.cancel') }}</AlertDialogCancel>
          <AlertDialogAction class="bg-destructive text-destructive-foreground hover:bg-destructive/90" @click="clearHistory">{{ t('pwaUi.logs.deleteAll') }}</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </LayoutSecond>
</template>
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import { Button } from '@/components/ui/button'
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription,
  AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { getOperationLogs, deleteOperationHistory, getContractLogResult } from '@/utils/operationLog'
import { generateMempoolUrl } from '@/utils'
import { useGlobalStore, useWalletStore } from '@/store'
import { Chain, Network } from '@/types/index'
import type { ContractLogResult, OperationView } from '@/utils/walletViews'
const {t, te} = useI18n()
const walletStore = useWalletStore()
const globalStore = useGlobalStore()
const records = ref<OperationView[]>([])
const results = ref<Partial<Record<string, ContractLogResult | { status: 'loading' }>>>({})
const rows = computed(() => records.value.map(record => ({ record, result: results.value[record.id] })))
const openRows = new Set<string>()
const confirmClear = ref(false)
const busy = ref(false); const error = ref('')
const kindLabel = (value: string) => te(`pwaUi.logs.actions.${value}`) ? t(`pwaUi.logs.actions.${value}`) : value
const statusLabel = (value: string) => te(`pwaUi.logs.states.${value}`) ? t(`pwaUi.logs.states.${value}`) : value
const operationStatus = (record: OperationView, status: string) =>
  record.contract && status === 'succeeded' ? t('pwaUi.logs.submitted') : statusLabel(status)
const contractTitle = (contract: NonNullable<OperationView['contract']>) => {
  if (!contract.url) return contract.action
  const [, asset, type] = contract.url.split('_')
  const ticker = asset?.split(':')[2]
  const name = type?.replace(/\.tc$/, '').toUpperCase() || t('pwaUi.logs.contractId')
  return [ticker, name, contract.action].filter(Boolean).join(' · ')
}
const chainLabel = (chain: Chain) => chain === Chain.BTC
  ? (walletStore.network === Network.TESTNET ? 'BTC Testnet4' : 'Bitcoin')
  : 'SatoshiNet'
const txLink = (txid: string, chain: Chain) => {
  if (!/^[a-f0-9]{64}$/i.test(txid)) return ''
  const network = walletStore.network
  const env = globalStore.env
  // The public SatoshiNet testnet serves transaction details under its API route.
  const path = chain === Chain.SATNET && network === Network.TESTNET && env === 'prd'
    ? `api/tx/${txid}`
    : `tx/${txid}`
  return generateMempoolUrl({ network, env, chain, path })
}
const readResult = async (record: OperationView) => {
  const cache = results.value
  if (!record.contract?.url || !record.tx_ids.length || cache[record.id]?.status === 'loading') return
  cache[record.id] = { status: 'loading' }
  try {
    cache[record.id] = await getContractLogResult(record.id)
  } catch {
    cache[record.id] = { status: 'unavailable' }
  }
}
const toggleDetails = (record: OperationView, event: Event) => {
  if ((event.currentTarget as HTMLDetailsElement).open) {
    openRows.add(record.id)
    void readResult(record)
  } else {
    openRows.delete(record.id)
  }
}
const run = async (action: () => Promise<void>) => { busy.value = true; error.value = ''; try { await action() } catch (cause) { error.value = String(cause) } finally { busy.value = false } }
const loadRecords = async () => {
  records.value = (await getOperationLogs()).filter(record => record.kind !== 'unlock_wallet' && record.kind !== 'lock_wallet')
  results.value = {}
  for (const record of records.value) {
    if (openRows.has(record.id)) void readResult(record)
  }
}
const refresh = () => run(loadRecords)
const clearHistory = () => run(async () => { await deleteOperationHistory(); await loadRecords() })
onMounted(refresh)
</script>
