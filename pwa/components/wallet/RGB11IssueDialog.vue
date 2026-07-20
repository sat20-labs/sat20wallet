<template>
  <Dialog :open="isOpen" @update:open="handleOpenChange">
    <DialogContent class="max-h-[90vh] w-[390px] overflow-y-auto rounded-lg bg-black"
      @pointer-down-outside="preventDismiss" @escape-key-down="preventDismiss">
      <DialogHeader>
        <DialogTitle>{{ $t('rgb11Transfer.issueTitle') }}</DialogTitle>
        <DialogDescription>{{ $t('rgb11Transfer.issueDescription') }}</DialogDescription>
      </DialogHeader>
      <div class="space-y-3 text-sm">
        <label class="block space-y-1">
          <span class="text-xs text-zinc-400">{{ $t('rgb11Transfer.schema') }}</span>
          <select v-model="schema" class="h-9 w-full rounded-md border border-zinc-700 bg-zinc-900 px-3">
            <option value="NIA">NIA</option>
            <option value="IFA">IFA</option>
            <option value="UDA">UDA</option>
          </select>
          <span class="block text-xs text-zinc-500">{{ $t(`rgb11Transfer.schemaHelp.${schema}`) }}</span>
        </label>
        <label class="block space-y-1">
          <span class="text-xs text-zinc-400">{{ $t('rgb11Transfer.ticker') }}</span>
          <Input v-model="ticker" maxlength="8" autocomplete="off" placeholder="USDT" />
          <span class="block text-xs text-zinc-500">{{ $t('rgb11Transfer.tickerHelp') }}</span>
        </label>
        <label class="block space-y-1">
          <span class="text-xs text-zinc-400">{{ $t('rgb11Transfer.name') }}</span>
          <Input v-model="name" maxlength="40" autocomplete="off" placeholder="Tether USD" />
          <span class="block text-xs text-zinc-500">{{ $t('rgb11Transfer.nameHelp') }}</span>
        </label>
        <label class="block space-y-1">
          <span class="text-xs text-zinc-400">{{ $t('rgb11Transfer.details') }}</span>
          <Input v-model="details" maxlength="255" autocomplete="off" />
          <span class="block text-xs text-zinc-500">{{ $t('rgb11Transfer.detailsHelp') }}</span>
        </label>
        <label class="block space-y-1">
          <span class="text-xs text-zinc-400">{{ $t('rgb11Transfer.precision') }}</span>
          <Input v-model="precision" type="number" min="0" max="18" />
          <span class="block text-xs text-zinc-500">{{ $t('rgb11Transfer.precisionHelp') }}</span>
        </label>
        <template v-if="schema !== 'UDA'">
          <label class="block space-y-1">
            <span class="text-xs text-zinc-400">
              {{ $t(advancedAllocations ? 'rgb11Transfer.initialAllocations' : 'rgb11Transfer.initialSupply') }}
            </span>
            <Input v-model="amounts" inputmode="decimal"
              :placeholder="advancedAllocations ? '1000, 2000' : '1000'" />
            <span class="block text-xs text-zinc-500">
              {{ $t(advancedAllocations ? 'rgb11Transfer.initialAllocationsHelp' : 'rgb11Transfer.initialSupplyHelp') }}
            </span>
          </label>
          <label class="flex items-center gap-2 text-xs text-zinc-400">
            <Checkbox :checked="advancedAllocations" @update:checked="setAdvancedAllocations" />
            {{ $t('rgb11Transfer.advancedAllocations') }}
          </label>
        </template>
        <template v-if="schema === 'IFA'">
          <label class="block space-y-1">
            <span class="text-xs text-zinc-400">{{ $t('rgb11Transfer.inflationAmounts') }}</span>
            <Input v-model="inflationAmounts" inputmode="decimal"
              :placeholder="advancedAllocations ? '9000, 1000' : '9000'" />
            <span class="block text-xs text-zinc-500">{{ $t('rgb11Transfer.inflationAmountsHelp') }}</span>
          </label>
          <label class="block space-y-1">
            <span class="text-xs text-zinc-400">{{ $t('rgb11Transfer.rejectListUrl') }}</span>
            <Input v-model="rejectListUrl" autocomplete="off" />
            <span class="block text-xs text-zinc-500">{{ $t('rgb11Transfer.rejectListUrlHelp') }}</span>
          </label>
        </template>
        <p class="text-xs text-zinc-500">
          {{ $t('rgb11Transfer.utxoUsage', { count: allocationCount }) }}
        </p>
        <p v-if="message" class="break-all text-xs"
          :class="warning ? 'text-amber-500' : success ? 'text-emerald-400' : 'text-red-400'">
          {{ message }}
        </p>
        <div v-if="issuedSummary" class="space-y-2 rounded-md border border-zinc-800 bg-zinc-950 p-3">
          <div>
            <p class="text-xs text-zinc-500">{{ $t('rgb11Transfer.issuedTicker') }}</p>
            <p class="break-all text-sm text-zinc-200">{{ issuedSummary.ticker }}</p>
          </div>
          <div>
            <p class="text-xs text-zinc-500">{{ $t('rgb11Transfer.issuedDisplayName') }}</p>
            <p class="break-all text-sm text-zinc-200">{{ issuedSummary.displayName }}</p>
          </div>
          <div>
            <p class="text-xs text-zinc-500">{{ $t('rgb11Transfer.issuedContractId') }}</p>
            <p class="break-all font-mono text-xs text-zinc-300">{{ issuedSummary.contractId }}</p>
          </div>
          <div>
            <p class="text-xs text-zinc-500">{{ $t('rgb11Transfer.issuedAssetName') }}</p>
            <p class="break-all font-mono text-xs text-zinc-300">{{ issuedSummary.assetName }}</p>
          </div>
          <Button variant="outline" class="w-full" @click="copyAssetName">
            {{ $t('rgb11Transfer.copyAssetName') }}
          </Button>
        </div>
        <div v-if="armor" class="space-y-2">
          <Textarea :model-value="armor" readonly spellcheck="false" class="min-h-32 bg-zinc-900 font-mono text-[10px]" />
          <Button variant="outline" class="w-full" @click="copyContract">
            {{ $t('rgb11Transfer.copyContract') }}
          </Button>
        </div>
        <Button class="w-full" :disabled="loading" @click="handlePrimaryAction">
          {{ loading
            ? $t('rgb11Transfer.issuing')
            : issuedSummary
              ? $t('rgb11Transfer.close')
              : $t('rgb11Transfer.issue') }}
        </Button>
        <p v-if="loading" class="text-center text-xs text-zinc-500">
          {{ $t('rgb11Transfer.issuingHelp') }}
        </p>
      </div>
    </DialogContent>
  </Dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useClipboard } from '@vueuse/core'
import walletManager from '@/utils/sat20'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'

const emit = defineEmits<{ (e: 'completed'): void }>()
const isOpen = defineModel('open', { type: Boolean })
const schema = ref<'NIA' | 'IFA' | 'UDA'>('NIA')
const ticker = ref('')
const name = ref('')
const details = ref('')
const precision = ref('0')
const amounts = ref('')
const inflationAmounts = ref('')
const advancedAllocations = ref(false)
const rejectListUrl = ref('')
const loading = ref(false)
const message = ref('')
const success = ref(false)
const warning = ref(false)
const armor = ref('')
const issuedSummary = ref<{
  ticker: string
  displayName: string
  contractId: string
  assetName: string
} | null>(null)
const { t } = useI18n()
const { copy } = useClipboard()

const handleOpenChange = (open: boolean) => {
  if (!open && loading.value) return
  isOpen.value = open
}

const preventDismiss = (event: Event) => event.preventDefault()

const handlePrimaryAction = () => {
  if (issuedSummary.value) {
    isOpen.value = false
    return
  }
  void runIssue()
}

const MAX_UINT64 = 18446744073709551615n

const atomicAmounts = (value: string, parsedPrecision: number): string[] | null => {
  if (!value.trim()) return []
  const values = advancedAllocations.value
    ? value.split(',').map((item) => item.trim()).filter(Boolean)
    : [value.trim()]
  if (!values.length || !Number.isInteger(parsedPrecision) || parsedPrecision < 0 || parsedPrecision > 18) {
    return null
  }
  const result: string[] = []
  for (const item of values) {
    const match = item.match(/^(0|[1-9][0-9]*)(?:\.([0-9]+))?$/)
    if (!match || (match[2]?.length || 0) > parsedPrecision) return null
    const atomic = `${match[1]}${(match[2] || '').padEnd(parsedPrecision, '0')}`.replace(/^0+(?=\d)/, '')
    const amount = BigInt(atomic || '0')
    if (amount <= 0n || amount > MAX_UINT64) return null
    result.push(amount.toString())
  }
  return result
}

const validTicker = (value: string) => /^[A-Za-z][A-Za-z0-9]{0,7}$/.test(value)
const validPrintableASCII = (value: string, min: number, max: number) => (
  value.length >= min && value.length <= max && /^[\x20-\x7e]+$/.test(value)
)

const formatAssetName = (value: any) => {
  const protocol = value?.Protocol || value?.protocol || 'rgb11'
  const type = value?.Type || value?.type || 'f'
  const assetTicker = value?.Ticker || value?.ticker || ''
  return assetTicker ? `${protocol}:${type}:${assetTicker}` : ''
}

const parsedPrecision = computed(() => Number(precision.value))
const parsedIssueAmounts = computed(() => schema.value === 'UDA'
  ? ['1']
  : atomicAmounts(amounts.value, parsedPrecision.value))
const parsedInflationAmounts = computed(() => schema.value === 'IFA'
  ? atomicAmounts(inflationAmounts.value, parsedPrecision.value)
  : [])
const allocationCount = computed(() => {
  if (schema.value === 'UDA') return 1
  return (parsedIssueAmounts.value?.length || 0) + (parsedInflationAmounts.value?.length || 0)
})

const setAdvancedAllocations = (enabled: boolean) => {
  advancedAllocations.value = enabled
  if (!enabled) {
    amounts.value = amounts.value.split(',')[0]?.trim() || ''
    inflationAmounts.value = inflationAmounts.value.split(',')[0]?.trim() || ''
  }
}

const runIssue = async () => {
  const issueAmounts = parsedIssueAmounts.value
  const inflation = parsedInflationAmounts.value
  const issuePrecision = parsedPrecision.value
  const normalizedTicker = ticker.value.trim()
  const normalizedName = name.value.trim()
  const normalizedDetails = details.value.trim()
  const normalizedRejectListURL = rejectListUrl.value.trim()
  const hasRequiredSupply = schema.value === 'UDA' ||
    (schema.value === 'NIA' && !!issueAmounts?.length) ||
    (schema.value === 'IFA' && !!(issueAmounts?.length || inflation?.length))
  if (!validTicker(normalizedTicker) || !validPrintableASCII(normalizedName, 1, 40) ||
    (normalizedDetails && !validPrintableASCII(normalizedDetails, 1, 255)) ||
    (normalizedRejectListURL && !validPrintableASCII(normalizedRejectListURL, 1, 8000)) ||
    issueAmounts === null || inflation === null || !hasRequiredSupply ||
    !Number.isInteger(issuePrecision) || issuePrecision < 0 || issuePrecision > 18) {
    message.value = t('rgb11Transfer.issueInvalid')
    success.value = false
    warning.value = false
    return
  }
  loading.value = true
  message.value = ''
  success.value = false
  warning.value = false
  armor.value = ''
  issuedSummary.value = null
  const [err, result] = await walletManager.issueRGB11Asset({
    schema: schema.value,
    ticker: normalizedTicker,
    name: normalizedName,
    details: normalizedDetails || undefined,
    precision: issuePrecision,
    amounts: issueAmounts,
    inflation_amounts: inflation,
    reject_list_url: normalizedRejectListURL || undefined,
    min_confirmations: 1,
  })
  if (err || !result?.result) {
    loading.value = false
    message.value = err?.message || t('rgb11Transfer.issueFailed')
    return
  }
  const issued = JSON.parse(result.result)
  armor.value = issued.armor || ''
  issuedSummary.value = {
    ticker: normalizedTicker,
    displayName: normalizedName,
    contractId: issued.contract_id || '',
    assetName: formatAssetName(issued.asset_name),
  }
  loading.value = false
  success.value = true
  warning.value = false
  message.value = t('rgb11Transfer.issued')
  emit('completed')
}

const copyContract = async () => {
  if (!armor.value) return
  await copy(armor.value)
  message.value = t('rgb11Transfer.copied')
  success.value = true
  warning.value = false
}

const copyAssetName = async () => {
  if (!issuedSummary.value?.assetName) return
  await copy(issuedSummary.value.assetName)
  message.value = t('rgb11Transfer.assetNameCopied')
  success.value = true
  warning.value = false
}

watch(isOpen, (open) => {
  if (!open) {
    schema.value = 'NIA'
    ticker.value = ''
    name.value = ''
    details.value = ''
    precision.value = '0'
    amounts.value = ''
    inflationAmounts.value = ''
    advancedAllocations.value = false
    rejectListUrl.value = ''
    loading.value = false
    message.value = ''
    success.value = false
    warning.value = false
    armor.value = ''
    issuedSummary.value = null
  }
})
</script>
