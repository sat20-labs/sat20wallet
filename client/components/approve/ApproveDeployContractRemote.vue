<template>
  <LayoutApprove @confirm="confirm" @cancel="cancel">
    <div class="p-4 space-y-4">
      <h2 class="text-2xl font-semibold text-center">{{$t('deployContractRemote.title', '合约远程部署确认')}}</h2>
      <p class="text-xs text-gray-400 text-center mb-4">
        {{$t('deployContractRemote.warning', '请确认以下合约部署信息，确认后将发起链上部署')}}
      </p>

      <div class="space-y-2">
        <div>
          <span class="font-semibold">{{$t('deployContractRemote.templateName', '合约模板')}}：</span>
          <span>{{ props.data?.templateName || '-' }}</span>
        </div>
        <div>
          <span class="font-semibold">{{$t('deployContractRemote.feeRate', '费率')}}：</span>
          <span>{{ props.data?.feeRate || '-' }}</span>
        </div>
        <div>
          <span class="font-semibold">{{$t('deployContractRemote.estimatedFee', '预估费用')}}：</span>
          <span v-if="feeLoading">{{$t('deployContractRemote.loading', '查询中...')}}</span>
          <span v-else-if="feeError" class="text-destructive">{{$t('deployContractRemote.feeError', '查询失败')}}</span>
          <span v-else>{{ estimatedFee || '-' }}</span>
        </div>
      </div>

      <!-- 合约参数美化展示 -->
      <Accordion type="single" collapsible class="w-full mt-2">
        <AccordionItem value="item-1">
          <AccordionTrigger class="text-sm">{{$t('deployContractRemote.viewRawContent', '查看原始合约参数')}}</AccordionTrigger>
          <AccordionContent>
            <Alert class="mt-2">
              <AlertTitle class="text-xs font-normal break-all whitespace-pre-wrap">
                {{ formattedContent }}
              </AlertTitle>
            </Alert>
          </AccordionContent>
        </AccordionItem>
      </Accordion>

      <div v-if="isLoading" class="text-center text-muted-foreground">
        <span class="animate-spin inline-block mr-2">⏳</span> {{$t('deployContractRemote.deploying', '正在部署合约...')}}
      </div>
      <div v-if="deployError && !isLoading" class="text-center text-destructive">
        {{ deployError }}
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
    templateName: string;
    content: string; // json string
    feeRate: string;
    [key: string]: any;
  }
}

const props = defineProps<Props>()
console.log('props', props)
const emit = defineEmits(['confirm', 'cancel'])
const toast = useToast()

const isLoading = ref(false)
const deployError = ref('')
const feeLoading = ref(false)
const feeError = ref(false)
const estimatedFee = ref<string>('')

const formattedContent = computed(() => {
  try {
    return JSON.stringify(JSON.parse(props.data?.content || '{}'), null, 2)
  } catch {
    return props.data?.content || '-'
  }
})

const getFee = async () => {
  if (!props.data?.templateName || !props.data?.content) {
    estimatedFee.value = '-'
    return
  }
  feeLoading.value = true
  feeError.value = false
  try {
    const [err, res] = await stp.getFeeForDeployContract(
      props.data.templateName,
      props.data.content,
      props.data.feeRate.toString()
    )
    console.log('getFeeForDeployContract', err, res)
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

watch(() => [props.data?.templateName, props.data?.content, props.data?.feeRate], getFee, { immediate: true })

const confirm = async () => {
  if (!props.data?.templateName || !props.data?.content) {
    toast.toast({
      title: '参数缺失',
      description: '合约模板、参数或费率缺失',
      variant: 'destructive',
    })
    return
  }
  isLoading.value = true
  deployError.value = ''
  try {
    const [err, res] = await stp.deployContract_Remote(
      props.data.templateName,
      props.data.content,
      props.data.feeRate.toString()
    )
    if (err) {
      deployError.value = err.message || '合约部署失败'
      toast.toast({
        title: '合约部署失败',
        description: err.message,
        variant: 'destructive',
      })
    } else if (res?.txId && res?.resvId) {
      emit('confirm', { txId: res.txId, resvId: res.resvId })
    } else {
      deployError.value = '合约部署返回异常'
      toast.toast({
        title: '合约部署异常',
        description: '未获取到部署结果',
        variant: 'destructive',
      })
    }
  } catch (e: any) {
    deployError.value = e?.message || '合约部署异常'
    toast.toast({
      title: '合约部署异常',
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