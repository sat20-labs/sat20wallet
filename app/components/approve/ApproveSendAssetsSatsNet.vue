<template>
  <LayoutApprove @confirm="confirm" @cancel="cancel" :loading="loading">
    <div class="space-y-4">
      <div v-if="isValidData" class="space-y-2">
        <h3 class="text-lg font-semibold">{{ $t('sendAssets.confirmTitle') }}</h3>
        <p class="text-sm text-muted-foreground">
          {{ $t('sendAssets.description') }}
        </p>
        <div class="rounded-md border p-4 space-y-3 bg-muted/50">
          <div>
            <p class="text-sm font-medium">
              {{ $t('sendAssets.assetName') }}: <strong>{{ props.data.assetName }}</strong>
            </p>
          </div>
          <div>
            <p class="text-sm font-medium">
              {{ $t('sendAssets.amount') }}: <strong>{{ props.data.amt }}</strong>
            </p>
          </div>
          <div>
            <p class="text-sm font-medium">
              {{ $t('sendAssets.destinationAddress') }}:
            </p>
            <div class="mt-1 text-xs font-mono bg-background p-2 rounded border break-all">
              {{ props.data.address }}
            </div>
          </div>
          <div v-if="props.data.memo">
            <p class="text-sm font-medium">
              {{ $t('sendAssets.memo') }}: <strong>{{ props.data.memo }}</strong>
            </p>
          </div>
        </div>
        <div class="text-sm text-muted-foreground pt-2">
          <p>{{ $t('sendAssets.feeNote') }}</p>
        </div>
      </div>
      <div v-else class="text-destructive">
        {{ $t('sendAssets.missingData') }}
      </div>
      <p v-if="errorMessage" class="text-sm text-destructive">{{ errorMessage }}</p>
    </div>
  </LayoutApprove>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { z } from 'zod'
import LayoutApprove from '@/components/layout/LayoutApprove.vue'
import { useToast } from '@/components/ui/toast-new'
import sat20 from '@/utils/sat20'

// Define Zod schema for validation
const sendAssetsSchema = z.object({
  address: z.string().min(1, 'Destination address is required'),
  assetName: z.string().min(1, 'Asset name is required'),
  amt: z.number().positive('Amount must be positive'),
  memo: z.string().optional()
})

// Infer TypeScript type from Zod schema
type SendAssetsData = z.infer<typeof sendAssetsSchema>

interface Props {
  data: SendAssetsData
}

const props = defineProps<Props>()
const emit = defineEmits(['confirm', 'cancel'])

const { toast } = useToast()
const loading = ref(false)
const errorMessage = ref<string | null>(null)

// Computed property to check data validity
const isValidData = computed(() => {
  const result = sendAssetsSchema.safeParse(props.data)
  return result.success
})

const validateData = () => {
  const result = sendAssetsSchema.safeParse(props.data)
  if (!result.success) {
    const errors = result.error.errors.map(e => e.message).join(', ')
    errorMessage.value = errors
    toast({
      title: 'Validation Error',
      description: errors,
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
    const [err, result] = await sat20.sendAssets_SatsNet(
      props.data.address,
      props.data.assetName,
      props.data.amt,
      props.data.memo || ""
    )

    if (err) {
      let detail = 'Failed to send assets on SatsNet.'
      if (err.message) {
        detail = err.message
      } else if (typeof err === 'string') {
        detail = err
      }
      throw new Error(detail)
    }

    toast({
      title: 'Success',
      description: `Successfully sent ${props.data.amt} ${props.data.assetName} to ${props.data.address}.`,
      variant: 'success'
    })

    emit('confirm', result)

  } catch (error: any) {
    console.error('Send Assets SatsNet Error:', error)
    const description = error.message || 'An unexpected error occurred during the send.'
    toast({
      title: 'Error',
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


