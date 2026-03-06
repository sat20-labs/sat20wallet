<template>
  <LayoutApprove @confirm="confirm" @cancel="cancel" :loading="loading">
    <div class="p-6 space-y-4">
      <div v-if="isValidData" class="space-y-2">
        <h3 class="text-lg font-semibold">{{ $t('sendGarbage.confirmTitle') }}</h3>
        <p class="text-sm text-muted-foreground">
          {{ $t('sendGarbage.description') }}
        </p>
        <div class="rounded-md border p-4 space-y-3 bg-muted/50">
          <div>
            <p class="text-sm font-medium">
              {{ $t('sendGarbage.destinationAddress') }}:
            </p>
            <div class="mt-1 text-xs font-mono bg-background p-2 rounded border break-all">
              {{ props.data.destAddr }}
            </div>
          </div>
          <div v-if="hasUtxos">
            <p class="text-sm font-medium">
              {{ $t('sendGarbage.selectedUtxos') }}: <strong>{{ utxosArray.length }}</strong>
            </p>
            <div class="mt-1 text-xs font-mono bg-background p-2 rounded border max-h-24 overflow-y-auto">
              <div v-for="(utxo, index) in utxosArray" :key="index" class="truncate">
                {{ utxo }}
              </div>
            </div>
          </div>
          <div>
            <p class="text-sm font-medium">
              {{ $t('sendGarbage.value') }}: <strong>{{ displayValue }} satoshis</strong>
            </p>
          </div>
          <div>
            <p class="text-sm font-medium">
              {{ $t('sendGarbage.feeRate') }}: <strong>{{ props.data.feeRate }} sat/vB</strong>
            </p>
          </div>
        </div>
        <div class="text-sm text-muted-foreground pt-2">
          <p>{{ $t('sendGarbage.warning') }}</p>
        </div>
      </div>
      <div v-else class="text-destructive">
        {{ $t('sendGarbage.missingData') }}
        <div class="mt-2 text-xs">
          Debug: destAddr={{ !!props.data?.destAddr }}, utxos={{ hasUtxos }}, value={{ hasValue }}, feeRate={{ !!props.data?.feeRate }}
        </div>
      </div>
      <p v-if="errorMessage" class="text-sm text-destructive">{{ errorMessage }}</p>
    </div>
  </LayoutApprove>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import LayoutApprove from '@/components/layout/LayoutApprove.vue'
import { useToast } from '@/components/ui/toast-new'
import sat20 from '@/utils/sat20'

interface SendGarbageData {
  destAddr: string
  utxos?: string[] | any
  value?: string | number
  feeRate: string | number
}

interface Props {
  data: SendGarbageData
}

const props = defineProps<Props>()
const emit = defineEmits(['confirm', 'cancel'])

const { toast } = useToast()
const loading = ref(false)
const errorMessage = ref<string | null>(null)

// 将 utxos 转换为数组
const utxosArray = computed(() => {
  if (!props.data?.utxos) return []
  if (Array.isArray(props.data.utxos)) return props.data.utxos
  return []
})

// 计算显示的 value
const displayValue = computed(() => {
  return props.data?.value ?? 0
})

// 检查是否有 utxos
const hasUtxos = computed(() => {
  return utxosArray.value.length > 0
})

// 检查是否有 value
const hasValue = computed(() => {
  const val = props.data?.value
  return val !== undefined && val !== null && val !== ''
})

// 数据验证
const isValidData = computed(() => {
  console.log('=== sendGarbage validation ===')
  console.log('props.data:', props.data)

  const hasDestAddr = props.data?.destAddr && props.data.destAddr.length > 0
  const hasFeeRate = props.data?.feeRate !== undefined && props.data?.feeRate !== null
  const validUtxos = hasUtxos.value
  const validValue = hasValue.value

  console.log('hasDestAddr:', hasDestAddr)
  console.log('hasFeeRate:', hasFeeRate)
  console.log('hasUtxos:', validUtxos)
  console.log('hasValue:', validValue)
  console.log('isValid:', hasDestAddr && hasFeeRate && (validUtxos || validValue))
  console.log('============================')

  return hasDestAddr && hasFeeRate && (validUtxos || validValue)
})

const validateData = () => {
  if (!isValidData.value) {
    errorMessage.value = 'Missing required data for garbage transaction.'
    return false
  }
  return true
}

const confirm = async () => {
  errorMessage.value = null

  if (!validateData()) {
    return
  }

  loading.value = true

  try {
    // 转换参数类型：value -> number, feeRate -> string
    const valueNum = typeof props.data.value === 'string' ? parseInt(props.data.value, 10) : (props.data.value || 0)
    const feeRateStr = String(props.data.feeRate)

    const [err, result] = await sat20.sendGarbage(
      props.data.destAddr,
      utxosArray.value,
      valueNum,
      feeRateStr
    )

    if (err) {
      let detail = 'Failed to send garbage transaction.'
      if (err.message) {
        detail = err.message
      } else if (typeof err === 'string') {
        detail = err
      }
      throw new Error(detail)
    }

    toast({
      title: 'Success',
      description: `Successfully sent ${valueNum} satoshis to ${props.data.destAddr}.`,
      variant: 'success'
    })

    emit('confirm', result)

  } catch (error: any) {
    console.error('Send Garbage Error:', error)
    const description = error.message || 'An unexpected error occurred during the transaction.'
    toast({
      title: 'Error',
      description: description,
      variant: 'destructive',
    })
    errorMessage.value = description
  } finally {
    loading.value = false
  }
}

const cancel = () => {
  emit('cancel')
}
</script>
