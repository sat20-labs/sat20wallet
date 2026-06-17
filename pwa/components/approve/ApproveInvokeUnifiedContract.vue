<template>
  <LayoutApprove @confirm="confirm" @cancel="cancel" :loading="isLoading">
    <div class="p-3 sm:p-4 space-y-3">
      <h2 class="text-lg sm:text-xl font-semibold text-center">
        {{ $t('invokeUnifiedContract.title', '智能合约调用确认') }}
      </h2>
      <p class="text-xs text-gray-400 text-center mb-2 sm:mb-3">
        {{ $t('invokeUnifiedContract.warning', '请确认以下智能合约调用信息，确认后将发起链上调用') }}
      </p>

      <div class="space-y-3">
        <div class="bg-muted/50 rounded-lg p-2 sm:p-3">
          <div class="flex items-center justify-between gap-2 py-1">
            <span class="text-xs text-muted-foreground flex-shrink-0">
              {{ $t('invokeUnifiedContract.contractType', '合约类型') }}
            </span>
            <span class="text-xs sm:text-sm font-medium text-right break-words">
              {{ contractType || '-' }}
            </span>
          </div>
          <div class="flex items-start justify-between gap-2 py-1">
            <span class="text-xs text-muted-foreground flex-shrink-0">
              {{ $t('invokeUnifiedContract.contractAddress', '合约地址') }}
            </span>
            <span class="text-xs font-medium text-right break-all leading-tight max-w-[60%]">
              {{ contractAddress || '-' }}
            </span>
          </div>
          <div v-if="betAssetName || betAmount" class="flex items-center justify-between gap-2 py-1">
            <span class="text-xs text-muted-foreground flex-shrink-0">
              {{ $t('invokeUnifiedContract.bet', '投注') }}
            </span>
            <span class="text-xs sm:text-sm font-medium text-right break-words">
              {{ betAmount || '-' }} {{ betAssetName || '' }}
            </span>
          </div>
        </div>

        <Accordion type="single" collapsible class="w-full">
          <AccordionItem value="item-1" class="border">
            <AccordionTrigger class="text-xs px-2 py-2 hover:no-underline sm:px-3">
              <span class="text-left">{{ $t('invokeUnifiedContract.viewRawContent', '查看原始调用参数') }}</span>
            </AccordionTrigger>
            <AccordionContent class="px-2 pb-2 sm:px-3 sm:pb-3">
              <Alert class="mt-1 border-0">
                <AlertTitle class="text-xs font-normal break-all whitespace-pre-wrap leading-relaxed p-2 -m-2 bg-muted/50 rounded">
                  {{ formattedRequest }}
                </AlertTitle>
              </Alert>
            </AccordionContent>
          </AccordionItem>
        </Accordion>
      </div>

      <div v-if="invokeError && !isLoading" class="text-center text-destructive mt-2">
        <span class="text-xs">{{ invokeError }}</span>
      </div>
    </div>
  </LayoutApprove>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import LayoutApprove from '@/components/layout/LayoutApprove.vue'
import { Alert, AlertTitle } from '@/components/ui/alert'
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from '@/components/ui/accordion'
import { useToast } from '@/components/ui/toast-new'
import { useWalletStore } from '@/store'
import sat20 from '@/utils/sat20'

interface Props {
  data: {
    req?: Record<string, any>
    [key: string]: any
  }
}

const props = defineProps<Props>()
const emit = defineEmits(['confirm', 'cancel'])
const { t } = useI18n()
const router = useRouter()
const toast = useToast()
const walletStore = useWalletStore()

const isLoading = ref(false)
const invokeError = ref('')

const req = computed(() => props.data?.req || {})
const agent = computed(() => req.value?.Agent || req.value?.agent || {})
const contractType = computed(() => req.value?.ContractType || req.value?.contractType || '')
const contractAddress = computed(() => agent.value?.ContractAddress || agent.value?.contractAddress || '')
const betAssetName = computed(() => agent.value?.BetAssetName || agent.value?.betAssetName || '')
const betAmount = computed(() => agent.value?.BetAmount || agent.value?.betAmount || '')

const formattedRequest = computed(() => JSON.stringify(req.value || {}, null, 2))

const walletLockedMessage = () => t('securitySetting.walletLockedError', '钱包未解锁，请先解锁钱包')

const isWalletLockedError = (message?: string) => {
  if (!message) return false
  return /wallet is not created\/unlocked|wallet.*unlock|钱包未解锁|未解锁/i.test(message)
}

const redirectToUnlock = async () => {
  if (!walletStore.hasWallet) return
  await router.push({
    path: '/unlock',
    query: { redirect: '/wallet/dapp' },
  })
}

const handleWalletLocked = async () => {
  const message = walletLockedMessage()
  invokeError.value = message
  toast.toast({
    title: t('common.error', '错误'),
    description: message,
    variant: 'destructive',
  })
  await redirectToUnlock()
}

const confirm = async () => {
  if (!req.value || !contractType.value) {
    toast.toast({
      title: t('invokeUnifiedContract.parameterMissing', '参数缺失'),
      description: t('invokeUnifiedContract.parameterMissingDescription', '智能合约调用参数缺失'),
      variant: 'destructive',
    })
    return
  }

  if (walletStore.hasWallet && walletStore.locked) {
    await handleWalletLocked()
    return
  }

  isLoading.value = true
  invokeError.value = ''
  try {
    const [err, res] = await sat20.invokeUnifiedContract(req.value)
    if (err) {
      if (isWalletLockedError(err.message)) {
        await handleWalletLocked()
        return
      }
      invokeError.value = err.message || '智能合约调用失败'
      toast.toast({
        title: t('invokeUnifiedContract.callFailed', '智能合约调用失败'),
        description: err.message,
        variant: 'destructive',
      })
      return
    }
    emit('confirm', res)
  } catch (e: any) {
    const message = e?.message || '智能合约调用异常'
    if (isWalletLockedError(message)) {
      await handleWalletLocked()
      return
    }
    invokeError.value = message
    toast.toast({
      title: t('invokeUnifiedContract.callException', '智能合约调用异常'),
      description: message,
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
