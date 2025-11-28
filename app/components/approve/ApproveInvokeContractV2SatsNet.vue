<template>
  <LayoutApprove @confirm="confirm" @cancel="cancel" :loading="isLoading">
    <div class="space-y-2 sm:space-y-3 max-w-full">
      <h2 class="text-lg sm:text-xl font-semibold text-center px-2">{{ $t('invokeContractSatsNet.title', '合约调用确认') }}</h2>
      <p class="text-xs text-gray-400 text-center mb-2 sm:mb-3 px-2 leading-relaxed">
        {{ $t('invokeContractSatsNet.warning', '请确认以下合约调用信息，确认后将发起链上调用') }}
      </p>

      <div class="space-y-2 sm:space-y-3">
        <!-- Basic Info Section -->
        <div class="bg-muted/50 rounded-lg p-2 sm:p-3">
          <div class="flex items-center justify-between gap-2 py-1">
            <span class="text-xs text-muted-foreground flex-shrink-0">{{ $t('invokeContractSatsNet.action', '操作类型') }}</span>
            <span class="text-xs sm:text-sm font-medium text-right break-words">{{ props.data?.metadata?.action || '-' }}</span>
          </div>
          <div class="flex items-start justify-between gap-2 py-1">
            <span class="text-xs text-muted-foreground flex-shrink-0">{{ $t('invokeContractSatsNet.url', '合约URL') }}</span>
            <span class="text-xs font-medium text-right break-all leading-tight max-w-[60%]">{{ props.data?.url || '-' }}</span>
          </div>
        </div>

        <!-- Transaction Details Section -->
        <div class="bg-muted/50 rounded-lg p-2 sm:p-3">
          <h3 class="text-xs font-medium text-muted-foreground mb-2">{{ $t('invokeContractSatsNet.transactionDetails',
            '交易详情') }}</h3>

          <!-- Swap Specific Details -->
          <template v-if="props.data?.metadata?.action === 'swap'">
            <div class="flex items-center justify-between gap-2 py-1">
              <span class="text-xs text-muted-foreground flex-shrink-0">{{ $t('invokeContractSatsNet.orderType', '订单类型') }}</span>
              <span class="text-xs sm:text-sm font-medium text-right break-words">{{ props.data?.metadata?.orderType === 1 ? $t('invokeContractSatsNet.sell',
                '卖出') : $t('invokeContractSatsNet.buy', '买入') }}</span>
            </div>
            <div class="flex items-center justify-between gap-2 py-1">
              <span class="text-xs text-muted-foreground flex-shrink-0">{{ $t('invokeContractSatsNet.quantity', '数量') }}</span>
              <span class="text-xs sm:text-sm font-medium text-right break-words">{{ props.data?.metadata?.quantity || '-' }}</span>
            </div>
            <div class="flex items-center justify-between gap-2 py-1">
              <span class="text-xs text-muted-foreground flex-shrink-0">{{ $t('invokeContractSatsNet.unitPrice', '单价') }}</span>
              <span class="text-xs sm:text-sm font-medium text-right break-words">{{ props.data?.metadata?.unitPrice || '-' }} sats</span>
            </div>
          </template>

          <div class="flex items-center justify-between gap-2 py-1">
            <span class="text-xs text-muted-foreground flex-shrink-0">{{ $t('invokeContractSatsNet.serviceFee', '网络费') }}</span>
            <span class="text-xs sm:text-sm font-medium text-right break-words">{{ props.data?.metadata?.networkFee || '-' }}
              sats</span>
          </div>
          <div class="flex items-center justify-between gap-2 py-1">
            <span class="text-xs text-muted-foreground flex-shrink-0">{{ $t('invokeContractSatsNet.estimatedFee', '服务费') }}</span>
            <div class="text-right">
              <span v-if="feeLoading" class="text-xs text-muted-foreground">{{ $t('invokeContractSatsNet.loading',
                '查询中...') }}</span>
              <span v-else-if="feeError" class="text-xs text-destructive break-words">{{ feeErrorMessage ||
                $t('invokeContractSatsNet.feeError',
                  '查询失败') }}</span>
              <span v-else class="text-xs sm:text-sm font-medium break-words">{{ estimatedFee || '-' }} sats</span>
            </div>
          </div>

          <!-- Total Cost -->
          <div class="flex items-center justify-between gap-2 py-2 border-t border-border">
            <span class="text-xs font-medium flex-shrink-0">{{ $t('invokeContractSatsNet.totalCost', '总花费') }}</span>
            <span class="text-xs sm:text-sm font-medium text-primary text-right break-words">{{ totalCost }}</span>
          </div>
          <div v-if="props.data?.metadata?.orderType === 1" class="flex items-center justify-between gap-2 py-2 border-t border-border">
            <span class="text-xs font-medium flex-shrink-0">{{ $t('invokeContractSatsNet.totalCost', '预估收入') }}</span>
            <span class="text-xs sm:text-sm font-medium text-primary text-right break-words">{{ props.data?.metadata?.sats }}</span>
          </div>
          <!-- <div class="flex items-center justify-between">
            <span class="text-sm text-muted-foreground">{{$t('invokeContractSatsNet.netFee', '网络费用')}}</span>
            <span class="font-medium">{{ props.data?.metadata?.netFeeSats || '-' }} sats</span>
          </div> -->
        </div>

        <!-- Contract Parameters Section -->
        <Accordion type="single" :collapsible="false" class="w-full">
          <AccordionItem value="item-1" class="border">
            <AccordionTrigger class="text-xs px-2 py-2 hover:no-underline sm:px-3">
              <span class="text-left">{{ $t('invokeContractSatsNet.viewRawContent', '查看原始调用参数') }}</span>
            </AccordionTrigger>
            <AccordionContent class="px-2 pb-2 sm:px-3 sm:pb-3">
              <Alert class="mt-1 border-0">
                <AlertTitle class="text-xs font-normal break-all whitespace-pre-wrap leading-relaxed p-2 -m-2 bg-muted/50 rounded">
                  {{ formattedInvoke }}
                </AlertTitle>
              </Alert>
            </AccordionContent>
          </AccordionItem>
        </Accordion>
      </div>

      <div v-if="isLoading" class="text-center text-muted-foreground mt-2 px-2">
        <span class="animate-spin inline-block mr-2">⏳</span>
        <span class="text-xs break-words">{{ $t('invokeContractSatsNet.invoking', '正在调用合约...') }}</span>
      </div>
      <div v-if="invokeError && !isLoading" class="text-center text-destructive mt-2 px-2">
        <span class="text-xs break-words">{{ invokeError }}</span>
      </div>
    </div>
  </LayoutApprove>
</template>

<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import LayoutApprove from '@/components/layout/LayoutApprove.vue'
import { Alert, AlertTitle } from '@/components/ui/alert'
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from '@/components/ui/accordion'
import { useToast } from '@/components/ui/toast-new'
import sat20 from '@/utils/sat20'
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
  const { orderType, quantity, unitPrice, networkFee = 10, sats } = props.data.metadata
  let total = 0;
  if (orderType === 2) {
    total = Math.ceil(Number(sats) + Number(networkFee) + Number(estimatedFee.value))
  } else  {
    total = Math.ceil(Number(estimatedFee.value) + Number(networkFee))
  }
  return `${total} sats`
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
    const [err, res] = await sat20.getFeeForInvokeContract(
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
      description: '合约URL、调用参数、资产名称、数量或费率缺失',
      variant: 'destructive',
    })
    return
  }
  isLoading.value = true
  invokeError.value = ''
  try {
    const [err, res] = await sat20.invokeContractV2_SatsNet(
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