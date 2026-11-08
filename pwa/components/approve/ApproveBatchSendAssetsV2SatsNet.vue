<template>
  <LayoutApprove @confirm="confirm" @cancel="cancel" :loading="loading">
    <div class="p-6 space-y-4">
      <div v-if="isValidData" class="space-y-2">
        <h3 class="text-lg font-semibold">{{ $t('batchSendAssetsV2.confirmTitle') }}</h3>
        <p class="text-sm text-muted-foreground">
          {{ $t('batchSendAssetsV2.description') }}
        </p>
        <div class="rounded-md border p-4 space-y-3 bg-muted/50">
          <div>
            <p class="text-sm font-medium">
              {{ $t('batchSendAssetsV2.assetName') }}: <strong>{{ assetDisplayName }}</strong>
            </p>
          </div>
          <div>
            <p class="text-sm font-medium">
              {{ $t('batchSendAssetsV2.batchCount') }}: <strong>{{ props.data.destAddr.length }}</strong>
            </p>
          </div>
          <div>
            <p class="text-sm font-medium">
              {{ $t('batchSendAssetsV2.destinationAddresses') }}:
            </p>
            <div class="mt-2 space-y-1">
              <div
                v-for="(addr, index) in props.data.destAddr"
                :key="index"
                class="text-xs font-mono bg-background p-2 rounded border break-all"
              >
                <div class="flex justify-between items-start">
                  <div class="flex-1">
                    <div class="font-semibold">{{ index + 1 }}. {{ $t('sendAssets.destinationAddress') }}:</div>
                    <div class="break-all">{{ addr }}</div>
                  </div>
                  <div class="ml-2 text-right">
                    <div class="font-semibold">{{ $t('sendAssets.amount') }}:</div>
                    <div>{{ props.data.amtList[index] || '0' }}</div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
        <div class="text-sm text-muted-foreground pt-2">
          <p>{{ $t('batchSendAssetsV2.totalAmount') }}: {{ totalAmount }}</p>
          <p>{{ $t('batchSendAssetsV2.feeNote') }}</p>
        </div>
      </div>
      <div v-else class="text-destructive">
        {{ $t('batchSendAssetsV2.missingData') }}
      </div>
      <p v-if="errorMessage" class="text-sm text-destructive">{{ errorMessage }}</p>
    </div>
  </LayoutApprove>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
const { t } = useI18n()
import { ref, computed, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { z } from 'zod'
import LayoutApprove from '@/components/layout/LayoutApprove.vue'
import { useWalletStore } from '@/store'
import { useToast } from '@/components/ui/toast-new'
import sat20 from '@/utils/sat20'

// Define Zod schema for validation
const batchSendAssetsV2Schema = computed(() => z.object({
  destAddr: z.array(z.string().min(1, t('approvalUi.addressEmpty'))),
  assetName: z.string().min(1, t('approvalUi.assetRequired')),
  amtList: z.array(z.string().min(1, t('approvalUi.amountEmpty'))).min(1, t('approvalUi.oneAmountRequired'))
}).refine((data) => data.destAddr.length === data.amtList.length, {
  message: t('approvalUi.countsMismatch'),
  path: ['amtList']
}))

// Infer TypeScript type from Zod schema
type BatchSendAssetsV2Data = z.infer<typeof batchSendAssetsV2Schema.value>

interface Props {
  data: BatchSendAssetsV2Data
}

const props = defineProps<Props>()
const emit = defineEmits(['confirm', 'cancel'])

const walletStore = useWalletStore()
const { toast } = useToast()

const loading = ref(false)
const errorMessage = ref<string | null>(null)

const assetDisplayName = ref<string>(props.data.assetName)

// Computed property to check data validity
const isValidData = computed(() => {
  const result = batchSendAssetsV2Schema.value.safeParse(props.data)
  return result.success
})

// Computed property for total amount calculation
const totalAmount = computed(() => {
  if (!props.data.amtList || props.data.amtList.length === 0) return '0'
  try {
    const total = props.data.amtList.reduce((sum, amt) => {
      const parsed = parseFloat(amt)
      return sum + (isNaN(parsed) ? 0 : parsed)
    }, 0)
    return total.toString()
  } catch {
    return '0'
  }
})

// Watch for asset name changes
watch(() => props.data.assetName, (newAssetName) => {
  if (newAssetName) {
    assetDisplayName.value = newAssetName
  }
}, { immediate: true })

const validateData = (): boolean => {
  const result = batchSendAssetsV2Schema.value.safeParse(props.data)

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

  if (!validateData()) {
    return
  }

  loading.value = true

  try {
    // Convert proxy arrays to plain arrays
    const destAddresses = [...props.data.destAddr]
    const amountList = [...props.data.amtList]

    const [err, result] = await sat20.batchSendAssetsV2_SatsNet(
      destAddresses,
      props.data.assetName,
      amountList
    )

    if (err) {
      let detail = t('approvalUi.sendFailed')
      if (err.message) {
        detail = err.message
      } else if (typeof err === 'string') {
        detail = err
      }
      throw new Error(detail)
    }

    toast({
      title: t('approvalUi.success'),
      description: t('approvalUi.batchSuccess', { count: destAddresses.length }),
      variant: 'success'
    })

    emit('confirm', result)

  } catch (error: any) {
    console.error('Batch Send Assets V2 Error:', error)
    const description = error.message || t('approvalUi.batchException')
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
