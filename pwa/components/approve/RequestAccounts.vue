<template>
  <LayoutApprove :loading="loading" @confirm="confirm" @cancel="cancel">
    <div class="p-4">
      <AccountCard :address="address" />
      <div class="mt-3 rounded-md border border-border p-3 text-xs">
        <p class="break-all"><span class="text-muted-foreground">DApp:</span> {{ origin }}</p>
        <p class="mt-2 text-muted-foreground">Requested permissions</p>
        <ul class="mt-1 list-disc pl-5">
          <li v-for="capability in capabilities" :key="capability">{{ capability }}</li>
        </ul>
        <p class="mt-2 text-muted-foreground">
          {{ props.data?.sessionOnly ? 'This browser session only' : 'Expires after 24 hours' }}
        </p>
      </div>
    </div>
  </LayoutApprove>
</template>

<script setup lang="ts">
import LayoutApprove from '@/components/layout/LayoutApprove.vue'
import AccountCard from '@/components/wallet/AccountCard.vue'
import { useApproveStore, useWalletStore } from '@/store'
import { storeToRefs } from 'pinia'
import service from '@/lib/service'
import { createDappGrant, getCurrentDappScope } from '@/lib/authorized-origins'
import { canonicalOrigin, normalizeCapabilities, type DappCapability } from '@/lib/dapp-grant-model'
import { computed, ref } from 'vue'
import { assertWalletIdentityReady } from '@/lib/identity-boundary'

interface Props {
  requestId: string
  data: any
  metadata: any
}

const props = defineProps<Props>()
const walletStore = useWalletStore()
const approveStore = useApproveStore()
const loading = ref(false)

const { address } = storeToRefs(walletStore)
const emit = defineEmits(['confirm', 'cancel'])
const origin = computed(() => canonicalOrigin(String(props.metadata?.origin ?? '')) ?? '')
const capabilities = computed<DappCapability[]>(() => {
  if (props.data?.capabilities === undefined) return ['accounts:read', 'public-key:read', 'network:read']
  return normalizeCapabilities(props.data.capabilities) ?? []
})

const confirm = async () => {
  if (loading.value) return
  const requestId = props.requestId
  const approvedOrigin = origin.value
  const approvedCapabilities = [...capabilities.value]
  const sessionOnly = Boolean(props.data?.sessionOnly)
  const generation = assertWalletIdentityReady(props.metadata?.identityGeneration)
  const assertValid = () => {
    assertWalletIdentityReady(generation)
    approveStore.assertCurrent(requestId)
  }
  loading.value = true
  try {
    assertValid()
    if (!approvedOrigin) throw new Error('Invalid DApp origin')
    if (!approvedCapabilities.length) throw new Error('Invalid DApp capabilities request')
    const accounts = await service.getAccounts()
    assertValid()
    const scope = await getCurrentDappScope()
    assertValid()
    const createdAt = Date.now()
    await createDappGrant({
      ...scope,
      origin: approvedOrigin,
      createdAt,
      expiresAt: createdAt + 24 * 60 * 60_000,
      capabilities: approvedCapabilities,
      sessionOnly,
    }, assertValid)
    assertValid()
    emit('confirm', accounts)
  } catch (error) {
    // Expiry or an identity transition can cancel this request while awaiting
    // the SDK. It is an expected cancellation, not an error for the next dialog.
    if (approveStore.currentRequest.value?.id !== requestId) return
    console.error('Request accounts approval failed')
    throw error
  } finally {
    loading.value = false
  }
}
const cancel = () => {
  emit('cancel')
}
</script>

<style lang="less" scoped></style>
