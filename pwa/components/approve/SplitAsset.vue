<template>
  <LayoutApprove @confirm="confirm" @cancel="cancel" :loading="loading">
    <div class="space-y-4">
      <div v-if="isValidData" class="space-y-2">
        <h3 class="text-lg font-semibold">{{ $t('splitAsset.confirmTitle') }}</h3>
        <p class="text-sm text-muted-foreground">
          {{ $t('splitAsset.description') }}
        </p>
        <div class="rounded-md border p-4 space-y-1 bg-muted/50">
           <p class="text-sm font-medium">
             {{ $t('splitAsset.assetKey') }}: <strong>{{ label }}</strong>
           </p>
           <p class="text-sm font-medium">
             {{ $t('splitAsset.amountToSplit') }}: <strong>{{ props.data.amt }}</strong>
           </p>
        </div>
        <p class="text-sm text-muted-foreground pt-2">
          {{ $t('splitAsset.utxoDescription') }}
        </p>
      </div>
      <div v-else class="text-destructive">
        {{ $t('splitAsset.missingData') }}
      </div>
      <p v-if="errorMessage" class="text-sm text-destructive">{{ errorMessage }}</p>
    </div>
  </LayoutApprove>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
const { t } = useI18n()
import { ref, computed, watch } from 'vue'
import { useL2Assets } from '@/composables/hooks/useL2Assets'
import { storeToRefs } from 'pinia'
import { z } from 'zod'
import LayoutApprove from '@/components/layout/LayoutApprove.vue'
import { useWalletStore } from '@/store'
import { useToast } from '@/components/ui/toast-new'
import walletManager from '@/utils/sat20'

// Define Zod schema for validation
const splitAssetSchema = computed(() => z.object({
  assetName: z.string().min(1, t('approvalUi.assetRequired')),
  amt: z.number().positive(t('approvalUi.amountPositive')),
  n: z.number()
}))

// Infer TypeScript type from Zod schema
type SplitAssetData = z.infer<typeof splitAssetSchema.value>

interface Props {
  data: SplitAssetData
}

const props = defineProps<Props>()
const emit = defineEmits(['confirm', 'cancel'])

const walletStore = useWalletStore()
const { address } = storeToRefs(walletStore)
const { refreshL2Assets, loading: l2Loading } = useL2Assets()
const { toast } = useToast()

const loading = ref(false)
const errorMessage = ref<string | null>(null)
const label = ref<string | null>(null)

watch(props.data, async (newData) => {
  const { assetName } = newData
  const [err, res] = await walletManager.getTickerInfo(assetName)
  if (res?.ticker) {
    const { ticker } = res
    const result = JSON.parse(ticker)
    label.value = result.name.Ticker || assetName
  }
}, { immediate: true })
// Computed property to check data validity
const isValidData = computed(() => {
  const result = splitAssetSchema.value.safeParse(props.data)
  return result.success
})

const validateData = (): boolean => {
  const result = splitAssetSchema.value.safeParse(props.data)

  if (!result.success) {
    const errors = result.error.errors.map(err => err.message).join(', ')
    errorMessage.value = t('approvalUi.validationFailed', { errors })
    toast({
      title: t('approvalUi.error'),
      description: errorMessage.value,
      variant: 'destructive'
    })
    return false
  }



  return true
}

const confirm = async () => {
  errorMessage.value = null

  if (l2Loading.value) {
     toast({
       title: t('approvalUi.info'),
       description: t('approvalUi.refreshingAssets'),
       variant: 'default'
     })
     return
  }

  if (!validateData()) {
    return
  }
  if (!address.value) {
    errorMessage.value = t('approvalUi.walletAddressRequired')
    toast({
      title: t('approvalUi.error'),
      description: errorMessage.value,
      variant: 'destructive'
    })
    return
  }
  loading.value = true

  try {
    const [err, result] = await walletManager.batchSendAssets_SatsNet(
      address.value,
      props.data.assetName,
      props.data.amt.toString(),
      props.data.n
    )

    if (err) {
      let detail = t('approvalUi.sendL2Failed')
      if (err.message) {
          detail = err.message
      } else if (typeof err === 'string') {
          detail = err
      }
      throw new Error(detail)
    }

    toast({
      title: t('approvalUi.success'),
      description: t('approvalUi.splitSuccess', { amount: props.data.amt, asset: props.data.assetName }),
      variant: 'success'
    })

    await refreshL2Assets()
    emit('confirm', result)

  } catch (error: any) {
    console.error('L2 Send Error (Split Asset):', error)
    const description = error.message || t('approvalUi.splitException')
    toast({
      title: t('approvalUi.error'),
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
