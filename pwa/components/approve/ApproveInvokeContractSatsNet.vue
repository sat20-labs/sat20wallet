<template>
  <LayoutApprove @confirm="confirm" @cancel="cancel">
    <div class="p-2 sm:p-3 space-y-2 sm:space-y-3 max-w-full">
      <h2 class="text-lg sm:text-xl font-semibold text-center px-2">{{ $t('invokeContractSatsNet.title', '合约调用确认') }}
      </h2>
      <p class="text-xs text-gray-400 text-center mb-2 px-2 leading-relaxed">
        {{ $t('invokeContractSatsNet.warning', '请确认以下合约调用信息，确认后将发起链上调用') }}
      </p>

      <!-- Basic Info Cards -->
      <div class="bg-muted/30 rounded-lg p-2 sm:p-3 space-y-2">
        <!-- URL -->
        <div class="flex items-start justify-between gap-2 py-1">
          <span class="text-xs font-semibold text-muted-foreground flex-shrink-0">{{ $t('invokeContractSatsNet.url',
            '合约URL') }}</span>
          <span class="text-xs font-medium break-all leading-tight max-w-[60%]">{{ props.data?.url || '-' }}</span>
        </div>

        <!-- Invoke Parameters -->
        <div class="flex items-start justify-between gap-2 py-1">
          <span class="text-xs font-semibold text-muted-foreground flex-shrink-0">{{ $t('invokeContractSatsNet.invoke',
            '调用参数') }}</span>
          <span class="text-xs font-medium break-all leading-tight max-w-[60%] text-right">{{ props.data?.invoke || '-'
          }}</span>
        </div>

        <!-- Fee Rate -->
        <div class="flex items-center justify-between gap-2 py-1">
          <span class="text-xs font-semibold text-muted-foreground flex-shrink-0">{{ $t('invokeContractSatsNet.feeRate',
            '费率') }}</span>
          <span class="text-xs sm:text-sm font-medium text-right">{{ props.data?.feeRate || '-' }} sats/TX</span>
        </div>

        <!-- Estimated Fee -->
        <div class="flex items-center justify-between gap-2 py-1">
          <span class="text-xs font-semibold text-muted-foreground flex-shrink-0">{{
            $t('invokeContractSatsNet.estimatedFee',
              '铸造费用') }}</span>
          <div class="text-right">
            <span v-if="feeLoading" class="text-xs text-muted-foreground">{{ $t('invokeContractSatsNet.loading',
              '查询中...') }}</span>
            <span v-else-if="feeError" class="text-xs text-destructive break-words">{{
              $t('invokeContractSatsNet.feeError', '查询失败') }}</span>
            <span v-else class="text-xs sm:text-sm font-medium break-words">{{ estimatedFee || '-' }} sats</span>
          </div>
        </div>
      </div>

      <!-- 合约参数美化展示 -->
      <Accordion type="single" :collapsible="false" class="w-full">
        <AccordionItem value="item-1" class="border">
          <AccordionTrigger class="text-xs px-2 py-2 hover:no-underline sm:px-3">
            <span class="text-left">{{ $t('invokeContractSatsNet.viewRawContent', '查看原始调用参数') }}</span>
          </AccordionTrigger>
          <AccordionContent class="px-2 pb-2 sm:px-3 sm:pb-3">
            <Alert class="mt-1 border-0">
              <AlertTitle
                class="text-xs font-normal break-all whitespace-pre-wrap leading-relaxed p-2 -m-2 bg-muted/50 rounded">
                {{ formattedInvoke }}
              </AlertTitle>
            </Alert>
          </AccordionContent>
        </AccordionItem>
      </Accordion>

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
    feeRate: string;
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
const estimatedFee = ref<string>('')

const formattedInvoke = computed(() => {
  try {
    return JSON.stringify(JSON.parse(props.data?.invoke || '{}'), null, 2)
  } catch {
    return props.data?.invoke || '-'
  }
})

const getFee = async () => {
  if (!props.data?.url || !props.data?.invoke) {
    estimatedFee.value = '-'
    return
  }
  feeLoading.value = true
  feeError.value = false
  try {
    const [err, res] = await sat20.getFeeForInvokeContract(
      props.data.url,
      props.data.invoke
    )
    if (err) {
      feeError.value = true
      estimatedFee.value = '-'
    } else {
      estimatedFee.value = res?.fee ? res.fee.toString() : '-'
    }
  } catch (e: any) {
    feeError.value = true
    estimatedFee.value = '-'
  } finally {
    feeLoading.value = false
  }
}

watch(() => [props.data?.url, props.data?.invoke, props.data?.feeRate], getFee, { immediate: true })

const confirm = async () => {
  if (!props.data?.url || !props.data?.invoke || !props.data?.feeRate) {
    toast.toast({
      title: '参数缺失',
      description: '合约URL、调用参数或费率缺失',
      variant: 'destructive',
    })
    return
  }
  isLoading.value = true
  invokeError.value = ''
  try {
    const [err, res] = await sat20.invokeContract_SatsNet(
      props.data.url,
      props.data.invoke,
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