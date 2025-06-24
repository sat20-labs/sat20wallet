<template>
  <LayoutApprove @confirm="confirm" @cancel="cancel">
    <div class="p-4 space-y-4">
      <h2 class="text-2xl font-semibold text-center">{{ $t('invokeContractSatsNet.title', '合约调用确认') }}</h2>
      <p class="text-xs text-gray-400 text-center mb-4">
        {{ $t('invokeContractSatsNet.warning', '请确认以下合约调用信息，确认后将发起链上调用') }}
      </p>

      <div class="space-y-4">
        <!-- Basic Info Section -->
        <div class="bg-muted/50 rounded-lg p-4 space-y-3">
          <div class="flex items-center justify-between">
            <span class="text-sm text-muted-foreground">{{ $t('invokeContractSatsNet.action', '操作类型') }}</span>
            <span class="font-medium">{{ props.data?.metadata?.action || '-' }}</span>
          </div>
          <!-- <div class="flex items-center justify-between">
            <span class="text-sm text-muted-foreground">{{ $t('invokeContractSatsNet.assetName', '资产名称') }}</span>
            <span class="font-medium">{{ props.data?.metadata?.assetName || props.data?.assetName || '-' }}</span>
          </div> -->
          <div class="flex items-center justify-between">
            <span class="text-sm text-muted-foreground">{{ $t('invokeContractSatsNet.url', '合约URL') }}</span>
            <span class="font-medium break-all text-right">{{ props.data?.url || '-' }}</span>
          </div>
        </div>

        <!-- Transaction Details Section -->
        <div class="bg-muted/50 rounded-lg p-4 space-y-3">
          <h3 class="text-sm font-medium text-muted-foreground mb-2">{{ $t('invokeContractSatsNet.transactionDetails',
            '交易详情') }}</h3>

          <!-- Swap Specific Details -->
          <template v-if="props.data?.metadata?.action === 'swap'">
            <div class="flex items-center justify-between">
              <span class="text-sm text-muted-foreground">{{ $t('invokeContractSatsNet.orderType', '订单类型') }}</span>
              <span class="font-medium">{{ props.data?.metadata?.orderType === 1 ? $t('invokeContractSatsNet.sell',
                '卖出') : $t('invokeContractSatsNet.buy', '买入') }}</span>
            </div>
            <div class="flex items-center justify-between">
              <span class="text-sm text-muted-foreground">{{ $t('invokeContractSatsNet.quantity', '数量') }}</span>
              <span class="font-medium">{{ props.data?.metadata?.quantity || '-' }}</span>
            </div>
            <div class="flex items-center justify-between">
              <span class="text-sm text-muted-foreground">{{ $t('invokeContractSatsNet.unitPrice', '单价') }}</span>
              <span class="font-medium">{{ props.data?.metadata?.unitPrice || '-' }} sats</span>
            </div>
          </template>

          <div class="flex items-center justify-between">
            <span class="text-sm text-muted-foreground">{{ $t('invokeContractSatsNet.serviceFee', '服务费') }}</span>
            <span class="font-medium">{{ props.data?.metadata?.serviceFee || props.data?.serviceFee || '-' }}
              sats</span>
          </div>
          <div class="flex items-center justify-between">
            <span class="text-sm text-muted-foreground">{{ $t('invokeContractSatsNet.feeRate', '费率') }}</span>
            <span class="font-medium">{{ props.data?.feeRate || '-' }} sats</span>
          </div>
          <div class="flex items-center justify-between">
            <span class="text-sm text-muted-foreground">{{ $t('invokeContractSatsNet.estimatedFee', '预估费用') }}</span>
            <span v-if="feeLoading" class="text-sm text-muted-foreground">{{ $t('invokeContractSatsNet.loading',
              '查询中...') }}</span>
            <span v-else-if="feeError" class="text-sm text-destructive">{{ feeErrorMessage ||
              $t('invokeContractSatsNet.feeError',
              '查询失败')}}</span>
            <span v-else class="font-medium">{{ estimatedFee || '-' }} sats</span>
          </div>
          
          <!-- Total Cost -->
          <div class="flex items-center justify-between border-t border-border pt-3 mt-3">
            <span class="text-sm font-medium">{{ $t('invokeContractSatsNet.totalCost', '总花费') }}</span>
            <span class="font-medium text-primary">{{ totalCost }}</span>
          </div>
          <!-- <div class="flex items-center justify-between">
            <span class="text-sm text-muted-foreground">{{$t('invokeContractSatsNet.netFee', '网络费用')}}</span>
            <span class="font-medium">{{ props.data?.metadata?.netFeeSats || '-' }} sats</span>
          </div> -->
        </div>

        <!-- Contract Parameters Section -->
        <Accordion type="single" :collapsible="false" class="w-full">
          <AccordionItem value="item-1">
            <AccordionTrigger class="text-sm">{{ $t('invokeContractSatsNet.viewRawContent', '查看原始调用参数') }}
            </AccordionTrigger>
            <AccordionContent>
              <Alert class="mt-2">
                <AlertTitle class="text-xs font-normal break-all whitespace-pre-wrap">
                  {{ formattedInvoke }}
                </AlertTitle>
              </Alert>
            </AccordionContent>
          </AccordionItem>
        </Accordion>
      </div>

      <div v-if="isLoading" class="text-center text-muted-foreground mt-4">
        <span class="animate-spin inline-block mr-2">⏳</span> {{ $t('invokeContractSatsNet.invoking', '正在调用合约...') }}
      </div>
      <div v-if="invokeError && !isLoading" class="text-center text-destructive mt-4">
        {{ invokeError }}
      </div>
    </div>
  </LayoutApprove>
</template>

<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import LayoutApprove from '@/components/layout/LayoutApprove.vue'
import { Alert, AlertTitle } from '@/components/ui/alert'
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from '@/components/ui/accordion'
import { useToast } from '@/components/ui/toast'
import stp from '@/utils/stp'

interface Props {
  data: {
    url: string;
    invoke: string; // json string or raw string
    assetName: string;
    amt: string;
    feeRate?: string;
    metadata?: any;
    [key: string]: any;
  }
}

const props = defineProps<Props>()
const emit = defineEmits(['confirm', 'cancel'])
const toast = useToast()

const isLoading = ref(false)
const invokeError = ref('')
const feeLoading = ref(false)
const feeError = ref(false)
const feeErrorMessage = ref('')
const estimatedFee = ref<string>('10')

const formattedInvoke = computed(() => {
  try {
    return JSON.stringify(JSON.parse(props.data?.invoke || '{}'), null, 2)
  } catch {
    return props.data?.invoke || '-'
  }
})

const totalCost = computed(() => {
  if (!props.data?.metadata?.action || !estimatedFee.value) return '-'
    const orderType = props.data.metadata.orderType
    if (orderType === 6) { // 卖出
      return `${Math.ceil(Number(estimatedFee.value))} sats` // 只有网络费，向上取整
    } else { // 买入
      const quantity = Number(props.data.metadata.quantity || 0)
      const unitPrice = Number(props.data.metadata.unitPrice || 0)
      const serviceFee = Number(props.data.metadata.serviceFee || props.data.serviceFee || 0)
      const networkFee = Number(estimatedFee.value || 0)
      
      const total = Math.ceil((quantity * unitPrice) + serviceFee + networkFee)
      return `${total.toLocaleString()} sats`
  }
  return '-'
})

const num = computed(() => {
  return props.data?.assetName === '::' ? Math.ceil(Number(props.data?.amt) / props.data?.unitPrice) : props.data?.amt
})

const getFee = async () => {
  if (!props.data?.url || !props.data?.invoke) {
    estimatedFee.value = '10'
    feeError.value = false
    feeErrorMessage.value = ''
    return
  }
  feeLoading.value = true
  feeError.value = false
  feeErrorMessage.value = ''
  try {
    const [err, res] = await stp.getFeeForInvokeContract(
      props.data.url,
      props.data.invoke
    )
    console.log('getFeeForInvokeContract', err, res)
    if (err) {
      feeError.value = true
      feeErrorMessage.value = err?.message || err?.toString?.() || '查询失败'
      estimatedFee.value = '10'
    } else {
      estimatedFee.value = res?.fee ? res.fee.toString() : '10'
    }
  } catch (e: any) {
    feeError.value = true
    feeErrorMessage.value = e?.message || e?.toString?.() || '查询失败'
    estimatedFee.value = '10'
  } finally {
    feeLoading.value = false
  }
}

watch(() => [props.data?.url, props.data?.invoke, props.data?.feeRate], getFee, { immediate: true })

const confirm = async () => {
  if (!props.data?.url || !props.data?.invoke || !props.data?.assetName || !props.data?.amt || !props.data?.feeRate) {
    toast.toast({
      title: '参数缺失',
      description: '合约URL、调用参数、资产名称、数量或费率64缺失',
      variant: 'destructive',
    })
    return
  }
  isLoading.value = true
  invokeError.value = ''
  try {
    const [err, res] = await stp.invokeContractV2(
      props.data.url,
      props.data.invoke,
      props.data.assetName,
      props.data.amt.toString(),
      props.data.feeRate.toString()
    )
    if (err) {
      invokeError.value = err.message || '合约调用失败'
      toast.toast({
        title: '合约调用失败',
        description: err.message,
        variant: 'destructive',
      })
    } else if (res?.txId) {
      emit('confirm', { txId: res.txId })
    } else {
      invokeError.value = '合约调用返回异常'
      toast.toast({
        title: '合约调用异常',
        description: '未获取到调用结果',
        variant: 'destructive',
      })
    }
  } catch (e: any) {
    invokeError.value = e?.message || '合约调用异常'
    toast.toast({
      title: '合约调用异常',
      description: e?.message,
      variant: 'destructive',
    })
  } finally {
    isLoading.value = false
  }
}

const cancel = () => {
  emit('cancel')
}
</script>