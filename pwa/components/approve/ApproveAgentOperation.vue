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

      <div v-if="riskAssessment.flags.length" class="rounded-md border border-amber-500/40 p-3 space-y-2 bg-amber-500/10">
        <p class="text-xs font-medium text-amber-300">Risk checks</p>
        <div v-for="flag in riskAssessment.flags" :key="flag.code" class="space-y-0.5">
          <p class="text-sm font-medium">{{ flag.label }}</p>
          <p class="text-xs text-muted-foreground">{{ flag.detail }}</p>
        </div>
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

      <label
        v-if="riskAssessment.requiresSecondConfirmation"
        class="flex items-start gap-2 rounded-md border border-destructive/40 p-3 text-sm"
      >
        <Checkbox
          :checked="riskAcknowledged"
          :disabled="loading"
          @update:checked="riskAcknowledged = $event"
        />
        <span>I reviewed the destination, amount, asset, origin, and risk checks for this Agent request.</span>
      </label>

      <p v-if="errorMessage" class="text-sm text-destructive">{{ errorMessage }}</p>
    </div>

    <template #footer>
      <div class="h-full grid grid-cols-2 gap-2 sm:gap-4">
        <Button
          variant="outline"
          :disabled="loading"
          class="text-sm sm:text-base h-full min-h-[44px] touch-manipulation"
          @click="cancel"
        >
          Cancel
        </Button>
        <Button
          :disabled="loading || (riskAssessment.requiresSecondConfirmation && !riskAcknowledged)"
          class="text-sm sm:text-base h-full min-h-[44px] touch-manipulation"
          @click="confirm"
        >
          Confirm
        </Button>
      </div>
    </template>
  </LayoutApprove>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import LayoutApprove from '@/components/layout/LayoutApprove.vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { useToast } from '@/components/ui/toast-new'
import { executePwaAgentOperation } from '@/composables/usePwaAgentAdapter'
import {
  assessAgentOperationRisk,
  stableAgentParamsHash,
} from '@/composables/usePwaAgentRiskPolicy'

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
const riskAcknowledged = ref(false)

const operation = computed(() => props.data.operation || '')
const params = computed(() => props.data.params || {})
const riskAssessment = computed(() => assessAgentOperationRisk(operation.value, params.value))

watch([operation, params], () => {
  riskAcknowledged.value = false
})

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
  if (riskAssessment.value.requiresSecondConfirmation && !riskAcknowledged.value) {
    errorMessage.value = 'Review and acknowledge the risk checks before confirming.'
    return
  }
  loading.value = true
  try {
    if (!operation.value) {
      throw new Error('Missing Agent operation.')
    }
    const result = await executePwaAgentOperation(operation.value, params.value, { approved: true })
    if (!result.ok) {
      throw new Error((result as any).error?.message || 'Agent operation failed.')
    }
    appendAgentAuditLog({
      operation: operation.value,
      params_hash: stableAgentParamsHash(operation.value, params.value),
      risk_flags: riskAssessment.value.flags.map((flag) => flag.code),
      risk_acknowledged: riskAcknowledged.value,
      confirmed_at: new Date().toISOString(),
    })
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

const appendAgentAuditLog = (entry: Record<string, any>) => {
  try {
    const key = 'sat20_agent_approval_audit'
    const current = JSON.parse(localStorage.getItem(key) || '[]')
    const next = Array.isArray(current) ? current : []
    next.push(entry)
    localStorage.setItem(key, JSON.stringify(next.slice(-100)))
  } catch (error) {
    console.warn('failed to record Agent approval audit log', error)
  }
}
</script>
