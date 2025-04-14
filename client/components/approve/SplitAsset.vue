<template>
  <LayoutApprove @confirm="confirm" @cancel="cancel" :loading="loading">
    <div class="p-6 space-y-4">
      <div v-if="props.data?.asset_key && props.data?.amount !== undefined" class="space-y-2">
        <h3 class="text-lg font-semibold">Confirm Asset Split</h3>
        <p class="text-sm text-muted-foreground">
          You are about to split the following asset:
        </p>
        <div class="rounded-md border p-4 space-y-1 bg-muted/50">
           <p class="text-sm font-medium">
             Asset Key: <strong>{{ props.data.asset_key }}</strong>
           </p>
           <p class="text-sm font-medium">
             Amount to Split: <strong>{{ props.data.amount }}</strong>
           </p>
        </div>
        <p class="text-sm text-muted-foreground pt-2">
            The specified amount will be sent back to your own address as a separate UTXO.
         </p>
      </div>
      <div v-else class="text-destructive">
        Required asset key or amount is missing.
      </div>
      <p v-if="errorMessage" class="text-sm text-destructive">{{ errorMessage }}</p>
    </div>
  </LayoutApprove>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { storeToRefs } from 'pinia'
import LayoutApprove from '@/components/layout/LayoutApprove.vue'
import { useWalletStore } from '@/store'
import { useToast } from '@/components/ui/toast/use-toast'
import satsnetStp from '@/utils/stp'

interface Props {
  data: {
    asset_key: string
    amount: number
  }
}

const props = defineProps<Props>()
const emit = defineEmits(['confirm', 'cancel'])

const walletStore = useWalletStore()
const { address } = storeToRefs(walletStore)
const { refreshL2Assets, loading: l2Loading } = useL2Assets()
const { toast } = useToast()

const loading = ref(false)
const errorMessage = ref<string | null>(null)

const confirm = async () => {
  errorMessage.value = null

  if (l2Loading.value) {
     toast({ title: 'Info', description: 'L2 assets are currently refreshing, please wait.', variant: 'default' });
     return;
  }
  if (!props.data?.asset_key || props.data.amount === undefined || props.data.amount <= 0 || !address.value) {
    const missing = []
    if (!props.data?.asset_key) missing.push('Asset Key')
    if (props.data?.amount === undefined || props.data.amount <= 0) missing.push('Valid Amount')
    if (!address.value) missing.push('Wallet Address')
    errorMessage.value = `Missing required information: ${missing.join(', ')}.`
    toast({ title: 'Error', description: errorMessage.value, variant: 'destructive' });
    return
  }

  loading.value = true

  try {
    const [err, result] = await satsnetStp.sendAssetsSatsNet(
      address.value,
      props.data.asset_key,
      Number(props.data.amount)
    )

    if (err) {
      let detail = 'Failed to send asset on L2.'
      if (err.message) {
          detail = err.message;
      } else if (typeof err === 'string') {
          detail = err;
      }
      throw new Error(detail)
    }

    toast({
      title: 'Success',
      description: `Successfully initiated split for ${props.data.amount} units of asset ${props.data.asset_key}.`,
    })

    await refreshL2Assets()
    emit('confirm', result)

  } catch (error: any) {
    console.error('L2 Send Error (Split Asset):', error)
    const description = error.message || 'An unexpected error occurred during the split.'
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
