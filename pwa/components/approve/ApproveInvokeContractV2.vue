<template>
  <LayoutApprove @confirm="confirm" @cancel="cancel">
    <div class="p-3 sm:p-4 space-y-3">
      <h2 class="text-lg sm:text-xl font-semibold text-center">{{ $t('invokeContractSatsNet.title') }}</h2>
      <p class="text-xs text-gray-400 text-center mb-2 sm:mb-3">
        {{ $t('invokeContractSatsNet.warning') }}
      </p>

      <div class="space-y-3">
        <!-- Basic Info Section -->
        <div class="bg-muted/50 rounded-lg p-2 sm:p-3">
          <div class="flex items-center justify-between gap-2 py-1">
            <span class="text-xs text-muted-foreground flex-shrink-0">{{ $t('invokeContractSatsNet.action')
              }}</span>
            <span class="text-xs sm:text-sm font-medium text-right break-words">{{ props.data?.metadata?.action || '-'
              }}</span>
          </div>
          <div class="flex items-start justify-between gap-2 py-1">
            <span class="text-xs text-muted-foreground flex-shrink-0">{{ $t('invokeContractSatsNet.url')
              }}</span>
            <span class="text-xs font-medium text-right break-all leading-tight max-w-[60%]">{{ props.data?.url || '-'
              }}</span>
          </div>
        </div>

        <!-- Transaction Details Section -->
        <div class="bg-muted/50 rounded-lg p-2 sm:p-3">
          <h3 class="text-xs font-medium text-muted-foreground mb-2">{{ $t('invokeContractSatsNet.transactionDetails') }}</h3>

          <!-- Swap Specific Details -->
          <template v-if="props.data?.metadata?.action === 'swap'">
            <div class="flex items-center justify-between gap-2 py-1">
              <span class="text-xs text-muted-foreground flex-shrink-0">{{ $t('invokeContractSatsNet.orderType')
                }}</span>
              <span class="text-xs sm:text-sm font-medium text-right break-words">{{ props.data?.metadata?.orderType ===
                1 ? $t('invokeContractSatsNet.sell') : $t('invokeContractSatsNet.buy') }}</span>
            </div>
            <div class="flex items-center justify-between gap-2 py-1">
              <span class="text-xs text-muted-foreground flex-shrink-0">{{ $t('invokeContractSatsNet.quantity')
                }}</span>
              <span class="text-xs sm:text-sm font-medium text-right break-words">{{ props.data?.metadata?.quantity ||
                '-' }}</span>
            </div>
            <div class="flex items-center justify-between gap-2 py-1">
              <span class="text-xs text-muted-foreground flex-shrink-0">{{ $t('invokeContractSatsNet.unitPrice')
                }}</span>
              <span class="text-xs sm:text-sm font-medium text-right break-words">{{ props.data?.metadata?.unitPrice ||
                '-' }} sats</span>
            </div>
          </template>

          <div class="flex items-center justify-between gap-2 py-1">
            <span class="text-xs text-muted-foreground flex-shrink-0">{{ $t('invokeContractSatsNet.serviceFee')
              }}</span>
            <span class="text-xs sm:text-sm font-medium text-right break-words">{{ props.data?.metadata?.networkFee ||
              '-' }}
              sats</span>
          </div>
          <div class="flex items-center justify-between gap-2 py-1">
            <span class="text-xs text-muted-foreground flex-shrink-0">{{ $t('invokeContractSatsNet.estimatedFee')
              }}</span>
            <div class="text-right">
              <span v-if="feeLoading" class="text-xs text-muted-foreground">{{ $t('invokeContractSatsNet.loading') }}</span>
              <span v-else-if="feeError" class="text-xs text-destructive">{{ feeErrorMessage ||
                $t('invokeContractSatsNet.feeError') }}</span>
              <span v-else class="text-xs sm:text-sm font-medium">{{ estimatedFee || '-' }} sats</span>
            </div>
          </div>

          <!-- Total Cost -->
          <div class="flex items-center justify-between gap-2 py-2 border-t border-border">
            <span class="text-xs font-medium flex-shrink-0">{{ $t('invokeContractSatsNet.totalCost') }}</span>
            <span class="text-xs sm:text-sm font-medium text-primary text-right break-words">{{ totalCost }}</span>
          </div>
          <!-- <div class="flex items-center justify-between">
            <span class="text-sm text-muted-foreground">{{$t('invokeContractSatsNet.netFee')}}</span>
            <span class="font-medium">{{ props.data?.metadata?.netFeeSats || '-' }} sats</span>
          </div> -->
        </div>

        <!-- Contract Parameters Section -->
        <Accordion type="single" :collapsible="false" class="w-full">
          <AccordionItem value="item-1" class="border">
            <AccordionTrigger class="text-xs px-2 py-2 hover:no-underline sm:px-3">
              <span class="text-left">{{ $t('invokeContractSatsNet.viewRawContent') }}</span>
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
      </div>

      <div v-if="isLoading" class="text-center text-muted-foreground mt-2">
        <span class="animate-spin inline-block mr-2">⏳</span> <span class="text-xs">{{
          $t('invokeContractSatsNet.invoking') }}</span>
      </div>
      <div v-if="invokeError && !isLoading" class="text-center text-destructive mt-2">
        <span class="text-xs">{{ invokeError }}</span>
      </div>
    </div>
  </LayoutApprove>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
const { t } = useI18n()
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
    const [err, res] = await sat20.getFeeForInvokeContract(
      props.data.url,
      props.data.invoke
    )
    console.log('getFeeForInvokeContract', err, res)
    if (err) {
      feeError.value = true
      feeErrorMessage.value = err?.message || err?.toString?.() || t('approvalUi.feeFailed')
      estimatedFee.value = '10'
    } else {
      estimatedFee.value = res?.fee ? res.fee.toString() : '10'
    }
  } catch (e: any) {
    feeError.value = true
    feeErrorMessage.value = e?.message || e?.toString?.() || t('approvalUi.feeFailed')
    estimatedFee.value = '10'
  } finally {
    feeLoading.value = false
  }
}

watch(() => [props.data?.url, props.data?.invoke, props.data?.feeRate], getFee, { immediate: true })

const confirm = async () => {
  if (!props.data?.url || !props.data?.invoke || !props.data?.assetName || !props.data?.amt || !props.data?.feeRate) {
    toast.toast({
      title: t('approvalUi.missingParameters'),
      description: t('approvalUi.missingAssetParameters'),
      variant: 'destructive',
    })
    return
  }
  isLoading.value = true
  invokeError.value = ''
  try {
    const [err, res] = await sat20.invokeContractV2(
      props.data.url,
      props.data.invoke,
      props.data.assetName,
      props.data.amt.toString(),
      props.data.feeRate.toString()
    )
    if (err) {
      invokeError.value = err.message || t('approvalUi.callFailed')
      toast.toast({
        title: t('approvalUi.callFailed'),
        description: err.message,
        variant: 'destructive',
      })
    } else if (res?.txId) {
      emit('confirm', { txId: res.txId })
    } else {
      invokeError.value = t('approvalUi.callInvalidResult')
      toast.toast({
        title: t('approvalUi.callException'),
        description: t('approvalUi.noCallResult'),
        variant: 'destructive',
      })
    }
  } catch (e: any) {
    invokeError.value = e?.message || t('approvalUi.callException')
    toast.toast({
      title: t('approvalUi.callException'),
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
