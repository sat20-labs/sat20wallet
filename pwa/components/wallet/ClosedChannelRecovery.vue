<template>
  <section v-if="matchingNotice" data-testid="closed-recovery-card" class="rounded-lg border p-3 my-3 space-y-2 text-sm">
    <p>{{ matchingNotice.message }}</p>
    <p v-if="assetRestorationOnly">The channel is READY. Asset restoration needs separate approval; no automatic account BTC transfer was made.</p>
    <template v-else>
      <p v-if="status" class="break-all">{{ status }}</p>
      <p v-if="recoveryError">{{ recoveryError }}</p>
      <Button :disabled="loading || acting || localReviewUnavailable" @click="requestClosedTopup" data-testid="recovery-choices-open">Review rebuild or withdrawal</Button>
    </template>
  </section>
  <p v-if="!matchingNotice && !channel && localCapabilityError" class="text-sm my-3">{{ localCapabilityError }}</p>
  <Dialog :open="showDialog" @update:open="(open) => { if (!open) cancelClosedTopup() }">
    <DialogContent data-testid="closed-recovery-dialog" class="sm:max-w-md">
      <DialogHeader>
        <DialogTitle>{{ withdrawalReview ? 'Confirm local withdrawal' : 'Choose closed-channel recovery' }}</DialogTitle>
        <DialogDescription>Only the currently selected account may confirm. Closing this window never sends a transaction.</DialogDescription>
      </DialogHeader>
      <div v-if="withdrawalReview" data-testid="withdraw-review" class="space-y-2 text-sm">
        <p class="break-all">Reviewed server parent: {{ withdrawalReview.options.oldParentId }}</p>
        <p>Already-signed recovery transactions: {{ withdrawalReview.options.withdrawTxCount }}</p>
        <p>Total miner fee already fixed in the signed successor transaction package (excluding the confirmed parent): {{ withdrawalReview.options.withdrawFeeSats }} sats</p>
        <p>This broadcasts only your complete already-signed local recovery package. It does not create a fresh wallet-funded transaction.</p>
      </div>
      <div v-else class="space-y-3 text-sm">
        <p v-if="recoveryError">{{ recoveryError }}</p>
        <div v-if="closedTopupReview" class="space-y-1">
          <p class="break-all">Channel: {{ closedTopupReview.details.channelId }}</p>
          <p>Account: {{ closedTopupReview.details.accountIndex }}</p>
          <p>New channel output: {{ closedTopupReview.details.amount }} sats</p>
          <p>Wallet transfer fee: {{ closedTopupReview.details.walletFee }} sats</p>
          <p>Total wallet debit: {{ closedTopupReview.details.requiredInput }} sats</p>
          <p>Fee rate: {{ closedTopupReview.details.feeRate }} sat/vB</p>
          <p>This is one full new UTXO, not your old balance's shortfall. Your old signed recovery package does not protect this new money. Rebuilding waits for this exact output to confirm; asset restoration may remain pending separately.</p>
          <p>If a payment is already recorded, confirmation only resumes that same transaction. If the server goes offline, do not add a second payment.</p>
        </div>
        <p v-if="optionsReview && !optionsReview.options.canWithdraw">Withdrawal unavailable: {{ optionsReview.options.withdrawReason }}</p>
        <Button :disabled="acting || !closedTopupReview" @click="confirmClosedTopup" data-testid="rebuild-confirm">Rebuild with this exact funding payment</Button>
        <Button :disabled="acting || !optionsReview?.options.canWithdraw || !!status" @click="reviewClosedWithdrawal" data-testid="withdraw-choice">Review withdrawal of old channel funds</Button>
      </div>
      <DialogFooter>
        <Button variant="secondary" @click="cancelClosedTopup" data-testid="recovery-cancel">Cancel</Button>
        <Button v-if="withdrawalReview" :disabled="acting" @click="confirmClosedWithdrawal" data-testid="withdraw-confirm">Broadcast this reviewed local package</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>

<script lang="ts" setup>
import { computed, ref, watch, onMounted, onBeforeUnmount } from 'vue'
import { storeToRefs } from 'pinia'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { useWalletStore, useChannelStore } from '@/store'
import { useToast } from '@/components/ui/toast-new'
import satsnetStp from '@/utils/stp'
import type { ClosedSingleUTXOTopupQuote, ClosedChannelRecoveryOptions } from '@/utils/stp'
import { assertWalletIdentityReady, getWalletIdentityState, subscribeWalletIdentity } from '@/lib/identity-boundary'

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
  if (channel.value || !local || local.walletId !== walletId.value || local.accountIndex !== accountIndex.value) return null
  return { walletId: local.walletId, accountIndex: local.accountIndex, channelId: local.channelId,
    message: local.hasLocalClosedState ? 'Local closed-channel recovery is available. Review your choices without sending money.' : local.withdrawReason, assetRestorationOnly: false }
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
const status = ref('')
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
const invalidate = () => { action++; acting.value = false; cancelClosedTopup(); loading.value = false; status.value = ''; recoveryError.value = ''; localRequest++; localRecovery.value = null; localCapabilityError.value = '' }
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
    if (capabilityError || !rawOptions) throw capabilityError || new Error('Local recovery capability is unavailable')
    const options = JSON.parse(rawOptions) as ClosedChannelRecoveryOptions
    if (!options || options.walletId !== walletId.value || options.accountIndex !== accountIndex.value || options.channelId !== channelId || typeof options.canWithdraw !== 'boolean') throw new Error('Invalid account-bound recovery capability')
    if (options.canWithdraw && (!options.oldParentId || !Number.isSafeInteger(options.withdrawFeeSats) || options.withdrawFeeSats < 0 || !Number.isSafeInteger(options.withdrawTxCount) || options.withdrawTxCount <= 0)) throw new Error('Incomplete local withdrawal capability')
    optionsReview.value = Object.freeze({ ...reviewContext, options: Object.freeze(options) })
    if (options.withdrawComplete) { recoveryError.value = options.withdrawReason; showDialog.value = true; return }
    const [quoteError, quote] = await satsnetStp.quoteClosedChannelSingleUTXOTopup(btcFeeRate.value)
    if (token !== request || !current(reviewContext)) return
    if (quoteError || !quote) recoveryError.value = quoteError?.message || 'Rebuild funding is currently unavailable'
    else {
      const details = JSON.parse(quote) as ClosedSingleUTXOTopupQuote
      if (!details || details.walletId !== walletId.value || details.accountIndex !== accountIndex.value || details.channelId !== channelId || details.feeRate !== btcFeeRate.value || !Number.isSafeInteger(details.amount) || details.amount <= 0 || !Number.isSafeInteger(details.walletFee) || details.walletFee < 0 || !Number.isSafeInteger(details.requiredInput) || details.requiredInput !== details.amount + details.walletFee || !details.quoteId || !details.unsignedTx) throw new Error('Invalid exact funding quote')
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
    if (typeof pending.txId !== 'string' || !pending.txId) throw new Error('Funding outcome is uncertain; check the original reservation before retrying')
    status.value = `Funding ${pending.txId} is ${pending.phase === 'broadcast' ? 'broadcast' : 'pending'}. Only this same transaction may be retried.`
    toast({ title: 'Funding recorded', description: status.value })
  } catch (error) { if (currentAction(token, review)) { recoveryError.value = error instanceof Error ? error.message : String(error); toast({ title: 'Rebuild paused', description: recoveryError.value, variant: 'destructive' }) } }
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
    status.value = `Withdrawal ${result?.resvId || ''} submitted. Waiting for the original signed package to confirm.`
    toast({ title: 'Withdrawal submitted', description: status.value })
  } catch (error) { if (currentAction(token, review)) { recoveryError.value = error instanceof Error ? error.message : String(error); toast({ title: 'Withdrawal paused', description: recoveryError.value, variant: 'destructive' }) } }
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
    if (error || !raw) throw error || new Error('Local recovery capability is unavailable; connect online or restore the required local material')
    const options = JSON.parse(raw) as ClosedChannelRecoveryOptions
    if (options.walletId !== walletId.value || options.accountIndex !== accountIndex.value || typeof options.channelId !== 'string' || typeof options.hasLocalClosedState !== 'boolean' || typeof options.canWithdraw !== 'boolean') throw new Error('Local recovery capability does not match this account')
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
