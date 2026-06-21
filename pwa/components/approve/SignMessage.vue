<template>
  <LayoutApprove @confirm="confirm" @cancel="cancel">
    <div class="p-4">
      <h2 class="text-2xl font-semibold text-center mb-4">{{ $t('signMessage.title') }}</h2>
      <p class="text-xs text-gray-400 text-center mb-2">
        {{ $t('signMessage.warning') }}
      </p>
      <p class="text-center text-base mb-2">{{ $t('signMessage.signing') }}</p>
      <div class="mb-3 rounded-md border p-3 space-y-2 bg-muted/50">
        <div class="flex items-center justify-between gap-3">
          <p class="text-xs text-muted-foreground">Signature payload</p>
          <span class="rounded-sm border px-2 py-0.5 text-xs font-medium">{{ signatureSummary.title }}</span>
        </div>
        <p v-if="signatureSummary.warning" class="text-xs text-destructive">{{ signatureSummary.warning }}</p>
        <div v-for="row in signatureSummary.rows" :key="row.key">
          <p class="text-xs text-muted-foreground">{{ row.key }}</p>
          <p class="text-sm font-mono break-all">{{ row.value }}</p>
        </div>
      </div>
      <Alert>
        <AlertTitle class="text-center text-base break-all">{{ props.data.message }}</AlertTitle>
      </Alert>
    </div>
  </LayoutApprove>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import LayoutApprove from '@/components/layout/LayoutApprove.vue'
import { Alert, AlertTitle } from '@/components/ui/alert'
import walletManager from '@/utils/sat20'
import { summarizeSignaturePayload } from '@/composables/usePwaAgentRiskPolicy'

interface Props {
  data: any
}

const props = defineProps<Props>()
const emit = defineEmits(['confirm', 'cancel'])

const signaturePayload = computed(() => props.data.message ?? props.data.data ?? '')
const signatureSummary = computed(() => summarizeSignaturePayload(signaturePayload.value))

const confirm = async () => {
  // await walletStore.setNetwork(props.data.network)
  const message = props.data.message ?? props.data.data ?? ''
  const [err, res] = props.data.signData
    ? await walletManager.signData(message)
    : await walletManager.signMessage(message)
  console.log(err, res);
  if (res) {
    emit('confirm', res)
  }

}
const cancel = () => {
  emit('cancel')
}
</script>

<style lang="less" scoped></style>
