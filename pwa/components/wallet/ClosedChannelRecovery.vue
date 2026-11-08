<template>
  <section v-if="matchingNotice" data-testid="closed-recovery-card" class="rounded-lg border p-3 my-3 space-y-2 text-sm">
    <p>{{ matchingNotice.message }}</p>
    <p v-if="assetRestorationOnly">{{ $t('walletComponents.restorationHelp') }}</p>
    <template v-else>
      <p v-if="status" class="break-all">{{ status }}</p>
      <p v-if="recoveryError">{{ recoveryError }}</p>
      <Button :disabled="loading || acting || localReviewUnavailable" @click="requestClosedTopup" data-testid="recovery-choices-open">{{ $t('walletComponents.reviewRecovery') }}</Button>
    </template>
  </section>
  <p v-if="!matchingNotice && !channel && localCapabilityError" class="text-sm my-3">{{ localCapabilityError }}</p>
  <Dialog :open="showDialog" @update:open="(open) => { if (!open) cancelClosedTopup() }">
    <DialogContent data-testid="closed-recovery-dialog" class="sm:max-w-md">
      <DialogHeader>
        <DialogTitle>{{ $t(withdrawalReview ? 'walletComponents.confirmWithdrawal' : 'walletComponents.chooseRecovery') }}</DialogTitle>
        <DialogDescription>{{ $t('walletComponents.recoveryAccountHelp') }}</DialogDescription>
      </DialogHeader>
      <div v-if="withdrawalReview" data-testid="withdraw-review" class="space-y-2 text-sm">
        <p class="break-all">{{ $t('walletComponents.reviewedParent', { id: withdrawalReview.options.oldParentId }) }}</p>
        <p>{{ $t('walletComponents.signedTransactions', { count: withdrawalReview.options.withdrawTxCount }) }}</p>
        <p>{{ $t('walletComponents.signedFees', { amount: withdrawalReview.options.withdrawFeeSats }) }}</p>
        <p>{{ $t('walletComponents.withdrawalHelp') }}</p>
      </div>
      <div v-else class="space-y-3 text-sm">
        <p v-if="recoveryError">{{ recoveryError }}</p>
        <div v-if="closedTopupReview" class="space-y-1">
          <p class="break-all">{{ $t('walletComponents.recoveryChannel', { id: closedTopupReview.details.channelId }) }}</p>
          <p>{{ $t('walletComponents.recoveryAccount', { index: closedTopupReview.details.accountIndex }) }}</p>
          <p>{{ $t('walletComponents.newOutput', { amount: closedTopupReview.details.amount }) }}</p>
          <p>{{ $t('walletComponents.walletTransferFee', { amount: closedTopupReview.details.walletFee }) }}</p>
          <p>{{ $t('walletComponents.walletDebit', { amount: closedTopupReview.details.requiredInput }) }}</p>
          <p>{{ $t('walletComponents.recoveryFeeRate', { rate: closedTopupReview.details.feeRate }) }}</p>
          <p>{{ $t('walletComponents.newFundingWarning') }}</p>
          <p>{{ $t('walletComponents.duplicateFundingWarning') }}</p>
        </div>
        <p v-if="optionsReview && !optionsReview.options.canWithdraw">{{ $t('walletComponents.withdrawalUnavailable', { reason: optionsReview.options.withdrawReason }) }}</p>
        <Button :disabled="acting || !closedTopupReview" @click="confirmClosedTopup" data-testid="rebuild-confirm">{{ $t('walletComponents.rebuildExact') }}</Button>
        <Button :disabled="acting || !optionsReview?.options.canWithdraw || !!status" @click="reviewClosedWithdrawal" data-testid="withdraw-choice">{{ $t('walletComponents.reviewWithdrawal') }}</Button>
      </div>
      <DialogFooter>
        <Button variant="secondary" @click="cancelClosedTopup" data-testid="recovery-cancel">{{ $t('walletComponents.cancel') }}</Button>
        <Button v-if="withdrawalReview" :disabled="acting" @click="confirmClosedWithdrawal" data-testid="withdraw-confirm">{{ $t('walletComponents.broadcastPackage') }}</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>

<script lang="ts" setup>
import { useI18n } from 'vue-i18n'
const { t } = useI18n()
import { computed, ref, watch, onMounted, onBeforeUnmount } from 'vue'
import { storeToRefs } from 'pinia'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { useWalletStore, useChannelStore } from '@/store'
import { useToast } from '@/components/ui/toast-new'
import satsnetStp from '@/utils/stp'
import type { ClosedSingleUTXOTopupQuote, ClosedChannelRecoveryOptions } from '@/utils/stp'
import { assertWalletIdentityReady, getWalletIdentityState, subscribeWalletIdentity } from '@/lib/identity-boundary'
import { wireWalletIdToStoreId } from '@/lib/wire-wallet-id'

const walletStore = useWalletStore()
const channelStore = useChannelStore()
const { walletId, accountIndex, network, btcFeeRate } = storeToRefs(walletStore)
const { channel, closedRebuildFundingNotice } = storeToRefs(channelStore)
const { toast } = useToast()
const generation = ref(getWalletIdentityState().generation)
const localRecovery = ref<Readonly<ClosedChannelRecoveryOptions> | null>(null)
const localCapabilityError = ref('')
const accountNotice = computed(() => {
  const notice = closedRebuildFundingNotice.value
  return notice?.walletId === walletId.value && notice.accountIndex === accountIndex.value ? notice : null
})
const matchingNotice = computed(() => {
  if (accountNotice.value) return accountNotice.value
  const local = localRecovery.value
  if (channel.value || !local?.hasLocalClosedState || wireWalletIdToStoreId(local.walletId) !== walletId.value || local.accountIndex !== accountIndex.value) return null
  // A deterministic channel locator exists before a channel is ever opened.
  // Missing local data alone is not evidence of a closed channel; a verified
  // local closed state or the account-bound server notice above is required.
  return { walletId: walletId.value, accountIndex: local.accountIndex, channelId: local.channelId,
    message: t('walletComponents.localRecoveryAvailable'), assetRestorationOnly: false }
})
const localReviewUnavailable = computed(() => !accountNotice.value && !localRecovery.value?.hasLocalClosedState)
const currentGenerationNotice = computed(() => {
  const notice = accountNotice.value
  return notice && (notice.identityGeneration === undefined || notice.identityGeneration === generation.value) ? notice : null
})
const assetRestorationOnly = computed(() => matchingNotice.value?.assetRestorationOnly || channel.value?.status === 16)
const showDialog = ref(false)
const loading = ref(false)
const acting = ref(false)
const statusMessage = ref<{ key: string; params: Record<string, string> } | null>(null)
const status = computed(() => statusMessage.value ? t(statusMessage.value.key, statusMessage.value.params) : '')
const recoveryError = ref('')
type ReviewContext = Readonly<{ generation: number; context: string }>
const closedTopupReview = ref<(ReviewContext & Readonly<{ quote: string; details: Readonly<ClosedSingleUTXOTopupQuote> }>) | null>(null)
const optionsReview = ref<(ReviewContext & Readonly<{ options: Readonly<ClosedChannelRecoveryOptions> }>) | null>(null)
const withdrawalReview = ref<typeof optionsReview.value>(null)
let request = 0
let localRequest = 0
let action = 0
let disposed = false
const contextKey = () => JSON.stringify([walletId.value, accountIndex.value, network.value, btcFeeRate.value, matchingNotice.value?.channelId])
const cancelClosedTopup = () => {
  request++
  showDialog.value = false
  closedTopupReview.value = null
  optionsReview.value = null
  withdrawalReview.value = null
}
// Invalidate only UI ownership. An approved original-account SDK reservation
// keeps running with its exact request, independently of the selected account.
const invalidate = () => { action++; acting.value = false; cancelClosedTopup(); loading.value = false; statusMessage.value = null; recoveryError.value = ''; localRequest++; localRecovery.value = null; localCapabilityError.value = '' }
watch([walletId, accountIndex, network, btcFeeRate], () => { invalidate(); void loadLocalRecovery() }, { flush: 'sync' })
const unsubscribe = subscribeWalletIdentity((state) => { generation.value = state.generation; invalidate(); if (state.phase === 'READY') void loadLocalRecovery() })
onBeforeUnmount(() => { disposed = true; invalidate(); unsubscribe() })
const current = (review: ReviewContext) => {
  try { assertWalletIdentityReady(review.generation); return !disposed && review.context === contextKey() }
  catch { return false }
}
const currentAction = (token: number, review: ReviewContext) => token === action && current(review)
const requestClosedTopup = async () => {
  if (loading.value || acting.value || disposed || !matchingNotice.value || assetRestorationOnly.value || localReviewUnavailable.value) return
  const token = ++request
  let capturedGeneration: number
  try { capturedGeneration = assertWalletIdentityReady() } catch { return }
  const reviewContext = { generation: capturedGeneration, context: contextKey() }
  const channelId = matchingNotice.value.channelId
  loading.value = true
  recoveryError.value = ''
  try {
    const [capabilityError, rawOptions] = await satsnetStp.previewClosedChannelRecovery(channelId)
    if (token !== request || !current(reviewContext)) return
    if (capabilityError || !rawOptions) throw capabilityError || new Error(t('walletComponents.localRecoveryUnavailable'))
    const options = JSON.parse(rawOptions) as ClosedChannelRecoveryOptions
    if (!options || wireWalletIdToStoreId(options.walletId) !== walletId.value || options.accountIndex !== accountIndex.value || options.channelId !== channelId || typeof options.canWithdraw !== 'boolean') throw new Error(t('walletComponents.invalidRecovery'))
    if (options.canWithdraw && (!options.oldParentId || !Number.isSafeInteger(options.withdrawFeeSats) || options.withdrawFeeSats < 0 || !Number.isSafeInteger(options.withdrawTxCount) || options.withdrawTxCount <= 0)) throw new Error(t('walletComponents.incompleteWithdrawal'))
    optionsReview.value = Object.freeze({ ...reviewContext, options: Object.freeze(options) })
    if (options.withdrawComplete) { recoveryError.value = options.withdrawReason; showDialog.value = true; return }
    const [quoteError, quote] = await satsnetStp.quoteClosedChannelSingleUTXOTopup(btcFeeRate.value)
    if (token !== request || !current(reviewContext)) return
    if (quoteError || !quote) recoveryError.value = quoteError?.message || t('walletComponents.fundingUnavailable')
    else {
      const details = JSON.parse(quote) as ClosedSingleUTXOTopupQuote
      if (!details || wireWalletIdToStoreId(details.walletId) !== walletId.value || details.accountIndex !== accountIndex.value || details.channelId !== channelId || details.feeRate !== btcFeeRate.value || !Number.isSafeInteger(details.amount) || details.amount <= 0 || !Number.isSafeInteger(details.walletFee) || details.walletFee < 0 || !Number.isSafeInteger(details.requiredInput) || details.requiredInput !== details.amount + details.walletFee || !details.quoteId || !details.unsignedTx) throw new Error(t('walletComponents.invalidQuote'))
      closedTopupReview.value = Object.freeze({ ...reviewContext, quote, details: Object.freeze(details) })
    }
    showDialog.value = true
  } catch (error) {
    if (token === request && !disposed) {
      recoveryError.value = error instanceof Error ? error.message : String(error)
      showDialog.value = true
    }
  } finally { if (token === request) loading.value = false }
}
const confirmClosedTopup = async () => {
  const review = closedTopupReview.value
  if (!review || acting.value || !current(review)) return
  const token = ++action
  acting.value = true
  cancelClosedTopup()
  try {
    const [error, result] = await satsnetStp.confirmClosedChannelSingleUTXOTopup(review.quote)
    if (error) throw error
    if (!currentAction(token, review)) return
    const pending = JSON.parse(result || '{}') as { txId?: unknown; phase?: unknown }
    if (typeof pending.txId !== 'string' || !pending.txId) throw new Error(t('walletComponents.fundingUncertain'))
    statusMessage.value = { key: pending.phase === 'broadcast' ? 'walletComponents.fundingBroadcast' : 'walletComponents.fundingPending', params: { txid: pending.txId } }
    toast({ title: t('walletComponents.fundingRecorded'), description: status.value })
  } catch (error) { if (currentAction(token, review)) { recoveryError.value = error instanceof Error ? error.message : String(error); toast({ title: t('walletComponents.rebuildPaused'), description: recoveryError.value, variant: 'destructive' }) } }
  finally { if (token === action) acting.value = false }
}
const reviewClosedWithdrawal = () => {
  const review = optionsReview.value
  if (!review?.options.canWithdraw || acting.value || !!status.value || !current(review)) return
  withdrawalReview.value = review
}
const confirmClosedWithdrawal = async () => {
  const review = withdrawalReview.value
  if (!review?.options.canWithdraw || acting.value || !current(review)) return
  const token = ++action
  acting.value = true
  cancelClosedTopup()
  try {
    const [error, result] = await satsnetStp.withdrawClosedChannelExact(review.options.channelId, review.options.oldParentId)
    if (error) throw error
    if (!currentAction(token, review)) return
    statusMessage.value = { key: 'walletComponents.withdrawalPending', params: { id: result?.resvId || '' } }
    toast({ title: t('walletComponents.withdrawalSubmitted'), description: status.value })
  } catch (error) { if (currentAction(token, review)) { recoveryError.value = error instanceof Error ? error.message : String(error); toast({ title: t('walletComponents.withdrawalPaused'), description: recoveryError.value, variant: 'destructive' }) } }
  finally { if (token === action) acting.value = false }
}
// Startup discovers only the captured current account's local state. It
// neither requests a funding quote nor opens a dialog or starts any action.
async function loadLocalRecovery() {
  const token = ++localRequest
  const context = JSON.stringify([walletId.value, accountIndex.value, network.value])
  let capturedGeneration: number
  try { capturedGeneration = assertWalletIdentityReady() } catch { return }
  try {
    const [error, raw] = await satsnetStp.previewClosedChannelRecovery('')
    assertWalletIdentityReady(capturedGeneration)
    if (disposed || token !== localRequest || context !== JSON.stringify([walletId.value, accountIndex.value, network.value])) return
    if (error || !raw) throw error || new Error(t('walletComponents.restoreLocalMaterial'))
    const options = JSON.parse(raw) as ClosedChannelRecoveryOptions
    if (!options || wireWalletIdToStoreId(options.walletId) !== walletId.value || options.accountIndex !== accountIndex.value || typeof options.channelId !== 'string' || typeof options.hasLocalClosedState !== 'boolean' || typeof options.canWithdraw !== 'boolean') throw new Error(t('walletComponents.recoveryAccountMismatch'))
    localRecovery.value = Object.freeze(options)
  } catch (error) {
    if (!disposed && token === localRequest && context === JSON.stringify([walletId.value, accountIndex.value, network.value])) localCapabilityError.value = error instanceof Error ? error.message : String(error)
  }
}
onMounted(() => { void loadLocalRecovery() })

// One notification opens one read-only choice review. Cancel never changes the
// notice, so it does not reopen or execute anything until an explicit review.
watch(currentGenerationNotice, (notice, previous) => { if (notice && notice !== previous && !assetRestorationOnly.value) void requestClosedTopup() }, { immediate: true, flush: 'post' })
</script>
