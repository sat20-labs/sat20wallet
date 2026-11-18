<template>
  <LayoutScroll>
    <div class="mx-auto max-w-2xl space-y-4 p-4">
      <h2 class="text-xl font-semibold">{{ t('pwaUi.logs.title') }}</h2>
      <p class="text-xs text-muted-foreground">{{ t('pwaUi.logs.description') }}</p>
      <Button variant="outline" :disabled="busy" @click="refresh">{{ t('common.refresh') }}</Button>
      <Button variant="outline" :disabled="busy" @click="clearHistory">{{ t('pwaUi.logs.deleteAll') }}</Button>
      <p v-if="error" role="alert" class="text-destructive">{{ error }}</p>
      <p v-if="!records.length">{{ t('pwaUi.logs.empty') }}</p>
      <details v-for="record in records" :key="record.id" class="space-y-2 rounded border p-3">
        <summary class="cursor-pointer">{{ kindLabel(record.kind) }} · {{ statusLabel(record.status) }}</summary>
        <p class="text-xs">{{ new Date(record.updated_at).toLocaleString() }}</p>
        <p v-for="(amount,index) in record.amounts" :key="index">{{ amount.amount }} {{ amount.asset }}</p>
        <p v-for="tx in record.tx_ids" :key="tx" class="break-all font-mono text-xs">{{ tx }}</p>
        <p v-if="record.error_code" class="break-all text-destructive">{{ record.error_code }}</p>
        <ol class="space-y-1 text-xs">
          <li v-for="(event,index) in record.history" :key="index">{{ new Date(event.time).toLocaleString() }} · {{ statusLabel(event.status) }} <span v-if="event.error_code">{{ event.error_code }}</span></li>
        </ol>
      </details>
    </div>
  </LayoutScroll>
</template>
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import LayoutScroll from '@/components/layout/LayoutScroll.vue'
import { Button } from '@/components/ui/button'
import { getOperationLogs, deleteOperationHistory } from '@/utils/operationLog'
import type { OperationView } from '@/utils/walletViews'
const {t, te} = useI18n()
const records = ref<OperationView[]>([])
const busy = ref(false); const error = ref('')
const kindLabel = (value: string) => te(`pwaUi.logs.actions.${value}`) ? t(`pwaUi.logs.actions.${value}`) : value
const statusLabel = (value: string) => te(`pwaUi.logs.states.${value}`) ? t(`pwaUi.logs.states.${value}`) : value
const run = async (action: () => Promise<void>) => { busy.value = true; error.value = ''; try { await action() } catch (cause) { error.value = String(cause) } finally { busy.value = false } }
const refresh = () => run(async () => { records.value = await getOperationLogs() })
const clearHistory = () => run(async () => { if (!window.confirm(t('pwaUi.logs.deleteConfirm'))) return; await deleteOperationHistory(); records.value = await getOperationLogs() })
onMounted(refresh)
</script>
