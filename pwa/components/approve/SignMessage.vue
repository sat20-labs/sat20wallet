<template>
  <LayoutApprove :confirm-disabled="!canSign" @confirm="confirm" @cancel="cancel">
    <div class="p-4 space-y-3">
      <h2 class="text-2xl font-semibold text-center">Sign SAT20 Wallet Message</h2>
      <p class="text-xs text-destructive text-center">Verify the requesting origin and digest before signing.</p>
      <div class="rounded-md border p-3 space-y-2 bg-muted/50 text-sm">
        <div><p class="text-xs text-muted-foreground">Domain</p><p class="font-mono">SAT20 Wallet Message / v1</p></div>
        <div><p class="text-xs text-muted-foreground">Origin</p><p class="font-mono break-all">{{ data.origin || 'unknown' }}</p></div>
        <div><p class="text-xs text-muted-foreground">Network</p><p class="font-mono">{{ data.network || 'unknown' }}</p></div>
        <div><p class="text-xs text-muted-foreground">Timestamp</p><p class="font-mono">{{ data.timestamp || 'unknown' }}</p></div>
        <div><p class="text-xs text-muted-foreground">Nonce</p><p class="font-mono break-all">{{ data.nonce || 'unknown' }}</p></div>
        <div><p class="text-xs text-muted-foreground">SHA-256 digest</p><p class="font-mono break-all">{{ data.digest || 'unknown' }}</p></div>
      </div>
      <Alert>
        <AlertTitle class="text-center text-base break-all">{{ data.message }}</AlertTitle>
      </Alert>
      <p v-if="!canSign" class="text-sm text-destructive text-center">Raw or incomplete DApp signing requests are refused.</p>
    </div>
  </LayoutApprove>
</template>

<script setup lang="ts">
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
  if (error || !result) throw error || new Error('Wallet did not return a message signature')
  assertWalletIdentityReady(props.metadata?.identityGeneration)
  emit('confirm', result)
}

const cancel = () => emit('cancel')
</script>
