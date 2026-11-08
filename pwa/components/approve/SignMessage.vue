<template>
  <LayoutApprove :confirm-disabled="!canSign" @confirm="confirm" @cancel="cancel">
    <div class="p-4 space-y-3">
      <h2 class="text-2xl font-semibold text-center">{{ t('approvalUi.signTitle') }}</h2>
      <p class="text-xs text-destructive text-center">{{ t('approvalUi.signReview') }}</p>
      <div class="rounded-md border p-3 space-y-2 bg-muted/50 text-sm">
        <div><p class="text-xs text-muted-foreground">{{ t('approvalUi.domain') }}</p><p class="font-mono">SAT20 Wallet Message / v1</p></div>
        <div><p class="text-xs text-muted-foreground">{{ t('approvalUi.origin') }}</p><p class="font-mono break-all">{{ data.origin || t('approvalUi.unknown') }}</p></div>
        <div><p class="text-xs text-muted-foreground">{{ t('approvalUi.network') }}</p><p class="font-mono">{{ data.network || t('approvalUi.unknown') }}</p></div>
        <div><p class="text-xs text-muted-foreground">{{ t('approvalUi.timestamp') }}</p><p class="font-mono">{{ data.timestamp || t('approvalUi.unknown') }}</p></div>
        <div><p class="text-xs text-muted-foreground">{{ t('approvalUi.nonce') }}</p><p class="font-mono break-all">{{ data.nonce || t('approvalUi.unknown') }}</p></div>
        <div><p class="text-xs text-muted-foreground">{{ t('approvalUi.digest') }}</p><p class="font-mono break-all">{{ data.digest || t('approvalUi.unknown') }}</p></div>
      </div>
      <Alert>
        <AlertTitle class="text-center text-base break-all">{{ data.message }}</AlertTitle>
      </Alert>
      <p v-if="!canSign" class="text-sm text-destructive text-center">{{ t('approvalUi.refuseRaw') }}</p>
    </div>
  </LayoutApprove>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
const { t } = useI18n()
import { computed } from 'vue'
import LayoutApprove from '@/components/layout/LayoutApprove.vue'
import { Alert, AlertTitle } from '@/components/ui/alert'
import walletManager from '@/utils/sat20'
import { assertWalletIdentityReady } from '@/lib/identity-boundary'

interface Props {
  data: {
    message?: string
    domainPayload?: string
    digest?: string
    origin?: string
    network?: string
    timestamp?: string
    nonce?: string
  }
  metadata?: { identityGeneration?: number }
}

const props = defineProps<Props>()
const emit = defineEmits(['confirm', 'cancel'])
const canSign = computed(() => Boolean(
  props.data.domainPayload && /^[0-9a-f]{64}$/.test(props.data.digest ?? '') &&
  props.metadata?.identityGeneration,
))

const confirm = async () => {
  if (!canSign.value || !props.data.domainPayload) return
  assertWalletIdentityReady(props.metadata?.identityGeneration)
  const [error, result] = await walletManager.signMessage(props.data.domainPayload)
  if (error || !result) throw error || new Error(t('approvalUi.noMessageSignature'))
  assertWalletIdentityReady(props.metadata?.identityGeneration)
  emit('confirm', { ...result, domainPayload: props.data.domainPayload })
}

const cancel = () => emit('cancel')
</script>
