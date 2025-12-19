<template>
  <LayoutApprove @confirm="confirm" @cancel="cancel">
    <div class="p-4 space-y-4">
      <h2 class="text-2xl font-semibold text-center">{{$t('invokeContractSatsNet.title', '合约调用确认')}}</h2>
      <p class="text-xs text-gray-400 text-center mb-4">
        {{$t('invokeContractSatsNet.warning', '请确认以下合约调用信息，确认后将发起链上调用')}}
      </p>

      <div class="space-y-2">
        <div>
          <span class="font-semibold">{{$t('invokeContractSatsNet.url', '合约URL')}}：</span>
          <span>{{ props.data?.url || '-' }}</span>
        </div>
        <div>
          <span class="font-semibold">{{$t('invokeContractSatsNet.invoke', '调用参数')}}：</span>
          <span>{{ props.data?.invoke || '-' }}</span>
        </div>
        <div>
          <span class="font-semibold">{{$t('invokeContractSatsNet.feeRate', '费率')}}：</span>
          <span>{{ props.data?.feeRate || '-' }} sats/TX</span>
        </div>
        <div>
          <span class="font-semibold">{{$t('invokeContractSatsNet.estimatedFee', '铸造费用')}}：</span>
          <span v-if="feeLoading">{{$t('invokeContractSatsNet.loading', '查询中...')}}</span>
          <span v-else-if="feeError" class="text-destructive">{{$t('invokeContractSatsNet.feeError', '查询失败')}}</span>
          <span v-else>{{ estimatedFee || '-' }} sats</span>
        </div>
      </div>

      <!-- 合约参数美化展示 -->
      <Accordion type="single" :collapsible="false" class="w-full mt-2">
        <AccordionItem value="item-1">
          <AccordionTrigger class="text-sm">{{$t('invokeContractSatsNet.viewRawContent', '查看原始调用参数')}}</AccordionTrigger>
          <AccordionContent>
            <Alert class="mt-2">
              <AlertTitle class="text-xs font-normal break-all whitespace-pre-wrap">
                {{ formattedInvoke }}
              </AlertTitle>
            </Alert>
          </AccordionContent>
        </AccordionItem>
      </Accordion>

      <div v-if="isLoading" class="text-center text-muted-foreground">
        <span class="animate-spin inline-block mr-2">⏳</span> {{$t('invokeContractSatsNet.invoking', '正在调用合约...')}}
      </div>
      <div v-if="invokeError && !isLoading" class="text-center text-destructive">
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