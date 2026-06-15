<template>
  <LayoutApprove @confirm="confirm" @cancel="cancel" :loading="loading">
    <div class="space-y-4">
      <div class="space-y-2">
        <h3 class="text-lg font-semibold">Agent Operation</h3>
        <p class="text-sm text-muted-foreground">
          Review this wallet or STP operation before allowing the Agent to execute it.
        </p>
      </div>

      <div class="rounded-md border p-3 space-y-1">
        <div class="flex items-center justify-between gap-3">
          <p class="text-xs text-muted-foreground">Risk category</p>
          <span class="rounded-sm border px-2 py-0.5 text-xs font-medium">{{ riskCategory.label }}</span>
        </div>
        <p class="text-sm text-muted-foreground">{{ riskCategory.description }}</p>
      </div>

      <div class="rounded-md border p-4 space-y-3 bg-muted/50">
        <div>
          <p class="text-xs text-muted-foreground">Operation</p>
          <p class="text-sm font-mono break-all">{{ operation }}</p>
        </div>
        <div v-for="item in summaryRows" :key="item.key">
          <p class="text-xs text-muted-foreground">{{ item.key }}</p>
          <p class="text-sm font-mono break-all">{{ item.value }}</p>
        </div>
      </div>

      <p v-if="errorMessage" class="text-sm text-destructive">{{ errorMessage }}</p>
    </div>
  </LayoutApprove>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import LayoutApprove from '@/components/layout/LayoutApprove.vue'
import { useToast } from '@/components/ui/toast-new'
import { executePwaAgentOperation } from '@/composables/usePwaAgentAdapter'

interface Props {
  data: {
    operation?: string
    params?: Record<string, any>
  }
}

const props = defineProps<Props>()
const emit = defineEmits(['confirm', 'cancel'])
const { toast } = useToast()

const loading = ref(false)
const errorMessage = ref<string | null>(null)

const operation = computed(() => props.data.operation || '')
const params = computed(() => props.data.params || {})

const riskCategory = computed(() => {
  const op = operation.value
  if (['wallet.status', 'stp.status', 'wallet.transaction', 'stp.transaction', 'stp.safety_snapshot', 'stp.commitment_export', 'stp.punish_status', 'stp.force_close_plan'].includes(op)) {
    return {
      label: 'Read-only safety',
      description: 'This operation reads wallet, transaction, channel, or safety evidence without moving assets.',
    }
  }
  if (['wallet.export_mnemonic', 'wallet.change_password', 'wallet.import', 'wallet.create'].includes(op)) {
    return {
      label: 'Secret-moving',
      description: 'This operation can create, reveal, import, or modify wallet secrets. Verify it carefully.',
    }
  }
  if (['stp.punish_build', 'stp.punish_broadcast', 'stp.sweep_build'].includes(op)) {
    return {
      label: 'Protective safety',
      description: 'This operation protects user assets during force-close or revoked-commitment recovery.',
    }
  }
  return {
    label: 'Value-moving',
    description: 'This operation may move BTC, SatoshiNet assets, or STP channel state. Check the asset, amount, destination, and channel.',
  }
})

const summaryRows = computed(() => {
  const keys = [
    'layer',
    'chain',
    'asset',
    'amount',
    'amount_sats',
    'to',
    'channel_id',
    'channel_point',
    'fee_rate',
    'force',
    'memo',
  ]
  return keys
    .filter((key) => params.value[key] !== undefined && params.value[key] !== '')
    .map((key) => ({
      key,
      value: Array.isArray(params.value[key])
        ? params.value[key].join(', ')
        : String(params.value[key]),
    }))
})

const confirm = async () => {
  errorMessage.value = null
  loading.value = true
  try {
    if (!operation.value) {
      throw new Error('Missing Agent operation.')
    }
    const result = await executePwaAgentOperation(operation.value, params.value, { approved: true })
    if (!result.ok) {
      throw new Error((result as any).error?.message || 'Agent operation failed.')
    }
    toast({
      title: 'Success',
      description: 'Agent operation executed.',
      variant: 'success',
    })
    emit('confirm', result)
  } catch (error: any) {
    const message = error?.message || String(error)
    errorMessage.value = message
    toast({
      title: 'Error',
      description: message,
      variant: 'destructive',
    })
  } finally {
    loading.value = false
  }
}

const cancel = () => {
  emit('cancel')
}
</script>
