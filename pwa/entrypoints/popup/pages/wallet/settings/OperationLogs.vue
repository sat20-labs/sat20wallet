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
      <details v-for="record in records" :key="record.id" class="rounded-xl border border-border bg-card p-3">
        <summary class="cursor-pointer text-sm font-medium">
          {{ kindLabel(record.kind) }} · {{ statusLabel(record.status) }}
          <span class="mt-1 block text-xs font-normal text-muted-foreground">{{ new Date(record.updated_at).toLocaleString() }}</span>
        </summary>
        <div class="mt-3 space-y-2 border-t border-border pt-3 text-sm">
          <p v-for="(amount,index) in record.amounts" :key="index">{{ amount.amount }} {{ amount.asset }}</p>
          <p v-for="tx in record.tx_ids" :key="tx" class="break-all font-mono text-xs">{{ tx }}</p>
          <p v-if="record.error_code" class="break-all text-destructive">{{ record.error_code }}</p>
          <ol class="space-y-1 text-xs text-muted-foreground">
            <li v-for="(event,index) in record.history" :key="index">{{ new Date(event.time).toLocaleString() }} · {{ statusLabel(event.status) }} <span v-if="event.error_code">{{ event.error_code }}</span></li>
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
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import { Button } from '@/components/ui/button'
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription,
  AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { getOperationLogs, deleteOperationHistory } from '@/utils/operationLog'
import type { OperationView } from '@/utils/walletViews'
const {t, te} = useI18n()
const records = ref<OperationView[]>([])
const confirmClear = ref(false)
const busy = ref(false); const error = ref('')
const kindLabel = (value: string) => te(`pwaUi.logs.actions.${value}`) ? t(`pwaUi.logs.actions.${value}`) : value
const statusLabel = (value: string) => te(`pwaUi.logs.states.${value}`) ? t(`pwaUi.logs.states.${value}`) : value
const run = async (action: () => Promise<void>) => { busy.value = true; error.value = ''; try { await action() } catch (cause) { error.value = String(cause) } finally { busy.value = false } }
const refresh = () => run(async () => { records.value = await getOperationLogs() })
const clearHistory = () => run(async () => { await deleteOperationHistory(); records.value = await getOperationLogs() })
onMounted(refresh)
</script>
