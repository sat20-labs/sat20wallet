<template>
  <main class="min-h-screen bg-background p-4 text-foreground">
    <h1 class="mb-4 text-xl font-semibold">{{ $t('pwaUi.agentSign.title') }}</h1>
    <p class="mb-3 text-sm text-muted-foreground">{{ $t('pwaUi.agentSign.reviewHint') }}</p>
    <textarea
      v-model="message"
      class="mb-3 h-36 w-full rounded border border-border bg-background p-2 text-sm"
      :placeholder="$t('pwaUi.agentSign.rawJson')"
    />
    <Button class="mb-3" :disabled="!message.trim() || signing" @click="reviewSignature">{{ $t('pwaUi.agentSign.review') }}</Button>

    <section v-if="review" class="mb-3 space-y-3 rounded border border-border p-3" :aria-label="$t('pwaUi.agentSign.confirmation')">
      <div>
        <h2 class="font-medium">{{ $t('pwaUi.agentSign.activeAccount') }}</h2>
        <dl class="mt-2 grid gap-2 text-xs">
          <div><dt class="text-muted-foreground">{{ $t('pwaUi.agentSign.wallet') }}</dt><dd>{{ review.walletName }}</dd></div>
          <div><dt class="text-muted-foreground">{{ $t('pwaUi.agentSign.account') }}</dt><dd>{{ $t('pwaUi.agentSign.accountIndex', { name: review.accountName, index: review.accountIndex }) }}</dd></div>
          <div><dt class="text-muted-foreground">{{ $t('pwaUi.agentSign.address') }}</dt><dd class="break-all font-mono">{{ review.address }}</dd></div>
          <div><dt class="text-muted-foreground">{{ $t('pwaUi.agentSign.network') }}</dt><dd>{{ review.network }}</dd></div>
        </dl>
      </div>

      <div>
        <h2 class="font-medium">{{ $t(review.summary.kind === 'structured' ? 'approvalUi.structuredSignature' : 'approvalUi.rawSignature') }}</h2>
        <p v-if="review.summary.warning" class="mt-1 text-xs text-destructive">{{ $t('approvalUi.opaqueWarning') }}</p>
        <dl v-if="review.summary.rows.length" class="mt-2 grid gap-2 text-xs">
          <div v-for="row in review.summary.rows" :key="row.key">
            <dt class="text-muted-foreground">{{ $t(`approvalUi.fields.${row.key}`) }}</dt>
            <dd class="break-all font-mono">{{ row.value }}</dd>
          </div>
        </dl>
      </div>

      <div>
        <h2 class="font-medium">{{ $t('pwaUi.agentSign.payload') }}</h2>
        <pre class="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-all rounded bg-muted/40 p-2 text-xs">{{ review.message }}</pre>
      </div>

      <div class="flex gap-2">
        <Button variant="outline" :disabled="signing" @click="cancelReview">{{ $t('pwaUi.agentSign.cancel') }}</Button>
        <Button :disabled="signing" @click="confirmSignature">{{ signing ? $t('pwaUi.agentSign.signing') : $t('pwaUi.agentSign.sign') }}</Button>
      </div>
    </section>

    <pre v-if="output" class="whitespace-pre-wrap break-all rounded border border-border p-3 text-xs">{{ output }}</pre>
  </main>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { ref } from 'vue'
import { Button } from '@/components/ui/button'
import { useWalletStore } from '@/store'
import { summarizeSignaturePayload, type AgentSignatureSummary } from '@/composables/usePwaAgentRiskPolicy'
import walletManager from '@/utils/sat20'

const { t } = useI18n()

interface SignatureReview {
  fingerprint: string
  message: string
  summary: AgentSignatureSummary
  walletName: string
  accountName: string
  accountIndex: number | string
  address: string
  network: string
}

const walletStore = useWalletStore()
const message = ref('')
const output = ref('')
const review = ref<SignatureReview | null>(null)
const signing = ref(false)

const currentContext = () => ({
  message: message.value,
  walletId: walletStore.walletId,
  accountIndex: walletStore.accountIndex,
  address: walletStore.address,
  network: walletStore.network,
})
const fingerprint = () => JSON.stringify(currentContext())

const reviewSignature = () => {
  if (!message.value.trim()) return
  const context = currentContext()
  review.value = {
    fingerprint: JSON.stringify(context),
    message: context.message,
    summary: summarizeSignaturePayload(context.message),
    walletName: walletStore.wallet?.name || walletStore.walletId || t('pwaUi.agentSign.unknownWallet'),
    accountName: walletStore.accounts?.find((account) => account.index === walletStore.accountIndex)?.name || t('pwaUi.agentSign.account'),
    accountIndex: walletStore.accountIndex ?? 'unknown',
    address: walletStore.address || t('pwaUi.agentSign.unavailable'),
    network: String(walletStore.network || 'unknown'),
  }
  output.value = ''
}

const cancelReview = () => {
  review.value = null
}

const confirmSignature = async () => {
  const snapshot = review.value
  if (!snapshot || signing.value) return
  if (snapshot.fingerprint !== fingerprint()) {
    output.value = t('pwaUi.agentSign.contextChanged')
    review.value = null
    return
  }
  signing.value = true
  try {
    const [err, res] = await walletManager.signData(snapshot.message)
    if (err) {
      output.value = err.message
      return
    }
    output.value = JSON.stringify(res, null, 2)
    review.value = null
  } finally {
    signing.value = false
  }
}
</script>
