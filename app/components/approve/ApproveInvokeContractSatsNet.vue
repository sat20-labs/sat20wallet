<template>
  <LayoutApprove @confirm="confirm" @cancel="cancel">
    <div class="p-3 sm:p-4 space-y-3 sm:space-y-4 max-w-full">
      <h2 class="text-xl sm:text-2xl font-semibold text-center px-2">{{$t('invokeContractSatsNet.title', '合约调用确认')}}</h2>
      <p class="text-xs text-gray-400 text-center mb-3 sm:mb-4 px-2 leading-relaxed">
        {{$t('invokeContractSatsNet.warning', '请确认以下合约调用信息，确认后将发起链上调用')}}
      </p>

      <!-- Basic Info Cards -->
      <div class="space-y-2 sm:space-y-3">
        <!-- URL Card -->
        <div class="bg-muted/30 rounded-lg p-3 sm:p-4">
          <div class="flex flex-col gap-1">
            <span class="text-sm font-semibold text-muted-foreground flex-shrink-0">{{$t('invokeContractSatsNet.url', '合约URL')}}</span>
            <span class="text-sm break-all leading-tight">{{ props.data?.url || '-' }}</span>
          </div>
        </div>

        <!-- Invoke Parameters Card -->
        <div class="bg-muted/30 rounded-lg p-3 sm:p-4">
          <div class="flex flex-col gap-1">
            <span class="text-sm font-semibold text-muted-foreground flex-shrink-0">{{$t('invokeContractSatsNet.invoke', '调用参数')}}</span>
            <span class="text-sm break-all leading-tight">{{ props.data?.invoke || '-' }}</span>
          </div>
        </div>

        <!-- Fee Rate Card -->
        <div class="bg-muted/30 rounded-lg p-3 sm:p-4">
          <div class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-1 sm:gap-0">
            <span class="text-sm font-semibold text-muted-foreground flex-shrink-0">{{$t('invokeContractSatsNet.feeRate', '费率')}}</span>
            <span class="text-sm font-medium text-right sm:text-left">{{ props.data?.feeRate || '-' }} sats/TX</span>
          </div>
        </div>

        <!-- Estimated Fee Card -->
        <div class="bg-muted/30 rounded-lg p-3 sm:p-4">
          <div class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-1 sm:gap-0">
            <span class="text-sm font-semibold text-muted-foreground flex-shrink-0">{{$t('invokeContractSatsNet.estimatedFee', '铸造费用')}}</span>
            <div class="text-right sm:text-left">
              <span v-if="feeLoading" class="text-sm text-muted-foreground">{{$t('invokeContractSatsNet.loading', '查询中...')}}</span>
              <span v-else-if="feeError" class="text-sm text-destructive break-words">{{$t('invokeContractSatsNet.feeError', '查询失败')}}</span>
              <span v-else class="text-sm font-medium break-words">{{ estimatedFee || '-' }} sats</span>
            </div>
          </div>
        </div>
      </div>

      <!-- 合约参数美化展示 -->
      <Accordion type="single" :collapsible="false" class="w-full">
        <AccordionItem value="item-1" class="border">
          <AccordionTrigger class="text-sm px-3 py-3 hover:no-underline sm:px-4">
            <span class="text-left">{{$t('invokeContractSatsNet.viewRawContent', '查看原始调用参数')}}</span>
          </AccordionTrigger>
          <AccordionContent class="px-3 pb-3 sm:px-4 sm:pb-4">
            <Alert class="mt-2 border-0">
              <AlertTitle class="text-xs font-normal break-all whitespace-pre-wrap leading-relaxed p-2 -m-2 bg-muted/50 rounded">
                {{ formattedInvoke }}
              </AlertTitle>
            </Alert>
          </AccordionContent>
        </AccordionItem>
      </Accordion>

      <div v-if="isLoading" class="text-center text-muted-foreground mt-3 sm:mt-4 px-2">
        <span class="animate-spin inline-block mr-2">⏳</span>
        <span class="text-sm break-words">{{$t('invokeContractSatsNet.invoking', '正在调用合约...')}}</span>
      </div>
      <div v-if="invokeError && !isLoading" class="text-center text-destructive mt-3 sm:mt-4 px-2">
        <span class="text-sm break-words">{{ invokeError }}</span>
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