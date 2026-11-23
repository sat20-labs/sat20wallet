<template>
  <Dialog :open="true" @update:open="open => { if (!open) emit('close') }">
    <DialogContent class="max-h-[calc(100dvh_-_2rem)] w-[calc(100%_-_2rem)] max-w-md grid-cols-[minmax(0,1fr)] gap-4 overflow-y-auto rounded-xl bg-background">
      <DialogHeader>
        <DialogTitle>{{ t('rgb11Contract.title') }}</DialogTitle>
        <DialogDescription class="break-words">{{ asset.name || asset.ticker }}</DialogDescription>
      </DialogHeader>
      <dl class="space-y-4 text-sm">
        <div>
          <dt class="text-muted-foreground">{{ t('rgb11Transfer.contractId') }}</dt>
          <dd class="mt-1 break-all font-mono text-xs">{{ asset.contract_id }}</dd>
        </div>
        <div v-if="asset.canonical_name">
          <dt class="text-muted-foreground">{{ t('rgb11Transfer.assetId') }}</dt>
          <dd class="mt-1 break-all font-mono text-xs">{{ asset.canonical_name }}</dd>
        </div>
        <div class="grid grid-cols-2 gap-3 rounded-lg border border-border p-3">
          <div v-for="field in issueFields" :key="field.label" class="min-w-0">
            <dt class="text-xs text-muted-foreground">{{ field.label }}</dt>
            <dd class="mt-1 break-words font-medium tabular-nums">{{ field.value }}</dd>
          </div>
        </div>
        <div v-if="info?.description">
          <dt class="text-muted-foreground">{{ t('rgb11Contract.description') }}</dt>
          <dd class="mt-1 break-words">{{ info.description }}</dd>
        </div>
        <div>
          <dt class="text-muted-foreground">{{ t('rgb11Contract.genesisOutput') }}</dt>
          <dd class="mt-1 break-all font-mono text-xs">
            <a v-if="txUrl" :href="txUrl" target="_blank" rel="noopener noreferrer"
              class="text-primary underline underline-offset-2">{{ outpoint }}</a>
            <span v-else-if="outpoint">{{ outpoint }}</span>
            <span v-else class="font-sans text-muted-foreground">{{ t(loading ? 'loading' : 'rgb11Contract.outputUnavailable') }}</span>
          </dd>
        </div>
      </dl>
      <p v-if="loadFailed" role="status" class="text-sm text-muted-foreground">{{ t('rgb11Contract.loadFailed') }}</p>
      <div class="flex flex-wrap gap-2 border-t border-border pt-4">
        <Button size="sm" variant="outline" class="h-auto min-h-10 whitespace-normal text-left"
          :disabled="exporting" @click="exportContract('copy')">
          {{ t('rgb11Transfer.copyContractArmor') }}
        </Button>
        <Button size="sm" variant="outline" class="h-auto min-h-10 whitespace-normal text-left"
          :disabled="exporting" @click="exportContract('save')">
          {{ t('rgb11Transfer.downloadContract') }}
        </Button>
      </div>
      <p v-if="notice" :role="notice.success ? 'status' : 'alert'" class="break-words text-xs"
        :class="notice.success ? 'text-emerald-400' : 'text-destructive'">{{ notice.text }}</p>
    </DialogContent>
  </Dialog>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { walletRequestSessionGuard } from '@/lib/walletSession'
import { useWalletStore } from '@/store/wallet'
import { Chain } from '@/types/index'
import { generateMempoolUrl } from '@/utils'
import { exportFileText } from '@/utils/accountManagement'
import { downloadRGB11ContractFile } from '@/utils/rgb11ContractFile'
import walletManager from '@/utils/sat20'
import type { RGBAssetView } from '@/utils/walletViews'

interface IssueInfo {
  totalMinted?: string
  maxSupply?: string
  description?: string
  content: string
}

interface GenesisInfo {
  contract_id: string
  ticker?: string
  genesis_outpoint?: string
}

const props = defineProps<{ asset: RGBAssetView }>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const wallet = useWalletStore()
const info = ref<IssueInfo>()
const genesis = ref<GenesisInfo>()
const loading = ref(true)
const loadFailed = ref(false)
const exporting = ref(false)
const notice = ref<{ success: boolean; text: string }>()

const supplyText = (raw?: string) => {
  if (!raw || !/^\d+$/.test(raw)) return ''
  const precision = props.asset.precision
  const digits = raw.padStart(precision + 1, '0')
  const split = digits.length - precision
  const whole = digits.slice(0, split).replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  const fraction = digits.slice(split).replace(/0+$/, '')
  return fraction ? `${whole}.${fraction}` : whole
}
const issueFields = computed(() => [
  { label: t('rgb11Contract.ticker'), value: genesis.value?.ticker },
  { label: t('rgb11Transfer.schema'), value: props.asset.kind },
  { label: t('rgb11Contract.precision'), value: String(props.asset.precision) },
  { label: t('rgb11Transfer.initialSupply'), value: supplyText(info.value?.totalMinted) },
  { label: t('rgb11Transfer.fixedSupply'), value: supplyText(info.value?.maxSupply) },
].filter(field => field.value))
// DeployTx in the generic ticker record can refer to another allocation.
// The naming origin is resolved specifically from the contract genesis.
const outpoint = computed(() => /^[0-9a-f]{64}:\d+$/i.exec(genesis.value?.genesis_outpoint || '')?.[0] || '')
const txUrl = computed(() => outpoint.value ? generateMempoolUrl({
  network: wallet.network,
  chain: Chain.BTC,
  path: `tx/${outpoint.value.split(':')[0]}`,
}) : '')

onMounted(async () => {
  try {
    const [error, result] = await walletManager.getTickerInfo(props.asset.asset)
    if (error || typeof result?.ticker !== 'string') {
      loadFailed.value = true
      return
    }
    const record = JSON.parse(result.ticker) as IssueInfo
    const bytes = Uint8Array.from(atob(record.content), value => value.charCodeAt(0))
    const origin = JSON.parse(new TextDecoder().decode(bytes)) as GenesisInfo
    if (origin.contract_id !== props.asset.contract_id) throw new Error('RGB contract mismatch')
    info.value = record
    genesis.value = origin
  } catch {
    loadFailed.value = true
  } finally {
    loading.value = false
  }
})

const exportContract = async (action: 'copy' | 'save') => {
  if (exporting.value) return
  exporting.value = true
  notice.value = undefined
  try {
    const checkSession = walletRequestSessionGuard('exportRGB11Contract')
    const readFile = async (extension: '.rgb' | '.asc') => {
      checkSession()
      const [error, files] = await walletManager.exportRGB11Contract(props.asset.asset)
      checkSession()
      if (error) throw error
      const file = files?.find(item => item.name.endsWith(extension))
      if (!file?.data) throw new Error(t('rgb11Transfer.exportContractFailed'))
      return file
    }
    if (action === 'copy') {
      if (!navigator.clipboard?.write || typeof ClipboardItem === 'undefined') {
        throw new Error(t('rgb11Transfer.contractArmorCopyFailed'))
      }
      // Start the clipboard write with the original click's user activation.
      const armor = readFile('.asc').then(file => new Blob([exportFileText(file)], { type: 'text/plain' }))
      await Promise.all([navigator.clipboard.write([new ClipboardItem({ 'text/plain': armor })]), armor])
      notice.value = { success: true, text: t('rgb11Transfer.contractArmorCopied') }
      return
    }
    const file = await readFile('.rgb')
    const filename = downloadRGB11ContractFile(props.asset.contract_id, file.data)
    notice.value = { success: true, text: t('common.fileDownloadStarted', { filename }) }
  } catch (error) {
    const text = action === 'copy' && error instanceof DOMException && error.name === 'NotAllowedError'
      ? t('rgb11Transfer.contractArmorCopyFailed')
      : error instanceof Error ? error.message : String(error)
    notice.value = { success: false, text }
  } finally {
    exporting.value = false
  }
}
</script>
