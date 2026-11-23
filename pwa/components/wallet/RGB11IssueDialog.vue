<template>
  <Dialog :open="isOpen" @update:open="handleOpenChange">
    <DialogContent class="max-h-[calc(100dvh_-_2rem)] w-[calc(100%_-_2rem)] max-w-md grid-cols-[minmax(0,1fr)] gap-4 overflow-y-auto rounded-xl bg-background"
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
          <span :id="`${inputId('ticker')}-label`" class="text-xs text-zinc-400">{{ $t('rgb11Transfer.ticker') }}</span>
          <Input v-model="ticker" v-bind="fieldAttrs('ticker')" maxlength="8" autocomplete="off"
            :placeholder="$t('rgb11Transfer.tickerExample')" />
          <span v-bind="hintAttrs('ticker')">{{ fieldHint('ticker', 'tickerHelp') }}</span>
        </label>
        <label class="block space-y-1">
          <span :id="`${inputId('name')}-label`" class="text-xs text-zinc-400">{{ $t('rgb11Transfer.name') }}</span>
          <Input v-model="name" v-bind="fieldAttrs('name')" maxlength="40" autocomplete="off" :placeholder="$t('walletComponents.nameExample')" />
          <span v-bind="hintAttrs('name')">{{ fieldHint('name', 'nameHelp') }}</span>
        </label>
        <label class="block space-y-1">
          <span :id="`${inputId('details')}-label`" class="text-xs text-zinc-400">{{ $t('rgb11Transfer.details') }}</span>
          <Input v-model="details" v-bind="fieldAttrs('details')" maxlength="255" autocomplete="off" />
          <span v-bind="hintAttrs('details')">{{ fieldHint('details', 'detailsHelp') }}</span>
        </label>
        <label class="block space-y-1">
          <span :id="`${inputId('precision')}-label`" class="text-xs text-zinc-400">{{ $t('rgb11Transfer.precision') }}</span>
          <Input v-model="precision" v-bind="fieldAttrs('precision')" type="number" min="0" max="18" />
          <span v-bind="hintAttrs('precision')">{{ fieldHint('precision', 'precisionHelp') }}</span>
        </label>
        <template v-if="schema !== 'UDA'">
          <label class="block space-y-1">
            <span :id="`${inputId('amounts')}-label`" class="text-xs text-zinc-400">
              {{ $t(`rgb11Transfer.${supplyField}`) }}
            </span>
            <Input v-model="amounts" v-bind="fieldAttrs('amounts')" inputmode="decimal"
              :placeholder="advancedAllocations ? '1000, 2000' : '1000'" />
            <span v-bind="hintAttrs('amounts')">{{ fieldHint('amounts', `${supplyField}Help`) }}</span>
          </label>
          <label class="flex items-center gap-2 text-xs text-zinc-400">
            <Checkbox :checked="advancedAllocations" @update:checked="setAdvancedAllocations" />
            {{ $t('rgb11Transfer.advancedAllocations') }}
          </label>
        </template>
        <template v-if="schema === 'IFA'">
          <label class="block space-y-1">
            <span :id="`${inputId('inflation')}-label`" class="text-xs text-zinc-400">{{ $t('rgb11Transfer.inflationAmounts') }}</span>
            <Input v-model="inflationAmounts" v-bind="fieldAttrs('inflation')" inputmode="decimal"
              :placeholder="advancedAllocations ? '9000, 1000' : '9000'" />
            <span v-bind="hintAttrs('inflation')">{{ fieldHint('inflation', 'inflationAmountsHelp') }}</span>
          </label>
          <label class="block space-y-1">
            <span :id="`${inputId('rejectUrl')}-label`" class="text-xs text-zinc-400">{{ $t('rgb11Transfer.rejectListUrl') }}</span>
            <Input v-model="rejectListUrl" v-bind="fieldAttrs('rejectUrl')" autocomplete="off" />
            <span v-bind="hintAttrs('rejectUrl')">{{ fieldHint('rejectUrl', 'rejectListUrlHelp') }}</span>
          </label>
        </template>
        <p class="text-xs text-zinc-500">
          {{ allocationCount > 0
            ? $t('rgb11Transfer.utxoUsage', { count: allocationCount })
            : $t('rgb11Transfer.utxoUsagePending') }}
        </p>
        <p v-if="!issuedSummary && !rgb11WriteAllowed" class="text-xs text-amber-400" role="status">
          {{ t('accountManagement.readiness.moduleBlocked') }}
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
          <Button v-if="contractConsignmentBase64" variant="outline" class="w-full" @click="downloadContract">
            {{ $t('rgb11Transfer.downloadContract') }}
          </Button>
          <Button variant="outline" class="w-full" @click="copyContract">
            {{ $t('rgb11Transfer.copyContract') }}
          </Button>
        </div>
        <p v-if="message" :role="success ? 'status' : 'alert'" class="break-words text-xs"
          :class="warning ? 'text-amber-500' : success ? 'text-emerald-400' : 'text-red-400'">
          {{ message }}
        </p>
        <Button class="w-full" :disabled="loading || (!issuedSummary && !rgb11WriteAllowed)" @click="handlePrimaryAction">
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
import { computed, nextTick, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { storeToRefs } from 'pinia'
import { useClipboard } from '@vueuse/core'
import walletManager from '@/utils/sat20'
import { useAccountManagementStore } from '@/store/accountManagement'
import { downloadRGB11ContractFile } from '@/utils/rgb11ContractFile'
import { exportFileText } from '@/utils/accountManagement'
import { validateSendAmount } from '@/utils/sendAmount'
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
const defaultNIASupply = '100000000'
const amounts = ref(defaultNIASupply)
const inflationAmounts = ref('')
const advancedAllocations = ref(false)
const rejectListUrl = ref('')
const loading = ref(false)
const message = ref('')
const success = ref(false)
const warning = ref(false)
const armor = ref('')
const contractConsignmentBase64 = ref('')
const issuedSummary = ref<{
  ticker: string
  displayName: string
  contractId: string
  assetName: string
} | null>(null)
const formId = useId()
const submitted = ref(false)
const { t } = useI18n()
const { copy } = useClipboard()
const accountManagementState = useAccountManagementStore()
const { rgb11WriteAllowed } = storeToRefs(accountManagementState)
const displayOperationError = (error: any, fallbackKey: string) => {
  const quotaKey = accountManagementState.quotaMessageKeyForCode(error?.code)
  if (quotaKey) return [error?.message, t(quotaKey)].filter(Boolean).join(' ')
  return error?.message || t(fallbackKey)
}

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

const decimalAmounts = (value: string): string[] => value.trim()
  ? (advancedAllocations.value ? value.split(',') : [value]).map(item => item.trim()).filter(Boolean) : []

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
const supplyField = computed(() => {
  if (advancedAllocations.value) return 'initialAllocations'
  return schema.value === 'NIA' ? 'fixedSupply' : 'initialSupply'
})
const parsedIssueAmounts = computed(() => schema.value === 'UDA'
  ? ['1']
  : decimalAmounts(amounts.value))
const parsedInflationAmounts = computed(() => schema.value === 'IFA'
  ? decimalAmounts(inflationAmounts.value)
  : [])
const parsedRejectUrl = computed(() => schema.value === 'IFA' ? rejectListUrl.value.trim() : '')
const allocationCount = computed(() => {
  if (schema.value === 'UDA') return 1
  return (parsedIssueAmounts.value?.length || 0) + (parsedInflationAmounts.value?.length || 0)
})

type IssueField = 'ticker' | 'name' | 'details' | 'precision' | 'amounts' | 'inflation' | 'rejectUrl'

const fieldErrors = computed<Partial<Record<IssueField, string>>>(() => {
  const errors: Partial<Record<IssueField, string>> = {}
  if (!submitted.value) return errors
  const symbol = ticker.value.trim()
  const displayName = name.value.trim()
  const description = details.value.trim()
  const digits = parsedPrecision.value
  const issue = parsedIssueAmounts.value
  const inflation = parsedInflationAmounts.value
  const rejectUrl = parsedRejectUrl.value
  if (!validTicker(symbol)) errors.ticker = symbol ? 'tickerInvalid' : 'tickerRequired'
  if (!validPrintableASCII(displayName, 1, 40)) errors.name = displayName ? 'nameInvalid' : 'nameRequired'
  if (description && !validPrintableASCII(description, 1, 255)) errors.details = 'detailsInvalid'
  const validPrecision = String(precision.value).trim() !== '' && Number.isInteger(digits) && digits >= 0 && digits <= 18
  if (!validPrecision) errors.precision = 'precisionInvalid'
  if (schema.value !== 'UDA') {
    if (!issue.length && !inflation.length) errors.amounts = schema.value === 'IFA' ? 'supplyEither' : 'supplyRequired'
    if (validPrecision) {
      // RGB stores each allocation as an unsigned 64-bit amount in atomic units.
      const limit = '18446744073709551615'
      const maximum = digits === 0 ? limit : `${limit.slice(0, -digits)}.${limit.slice(-digits)}`
      const invalidAmount = (value: string) => !!validateSendAmount(value.replace(/^\+/, ''), digits, maximum)
      if (issue.some(invalidAmount)) errors.amounts = 'supplyInvalid'
      if (inflation.some(invalidAmount)) errors.inflation = 'supplyInvalid'
    }
  }
  if (rejectUrl && !validPrintableASCII(rejectUrl, 1, 8000)) errors.rejectUrl = 'rejectUrlInvalid'
  return errors
})

const inputId = (field: IssueField) => `${formId}-${field}`
const fieldAttrs = (field: IssueField) => ({
  id: inputId(field),
  'aria-labelledby': `${inputId(field)}-label`,
  'aria-invalid': Boolean(fieldErrors.value[field]),
  'aria-describedby': `${inputId(field)}-hint`,
})
const hintAttrs = (field: IssueField) => ({
  id: `${inputId(field)}-hint`,
  class: fieldErrors.value[field] ? 'block text-xs text-red-400' : 'block text-xs text-zinc-500',
})
const fieldHint = (field: IssueField, help: string) => t(`rgb11Transfer.${fieldErrors.value[field] || help}`)

const setAdvancedAllocations = (enabled: boolean) => {
  advancedAllocations.value = enabled
  if (!enabled) {
    amounts.value = amounts.value.split(',')[0]?.trim() || ''
    inflationAmounts.value = inflationAmounts.value.split(',')[0]?.trim() || ''
  }
}

const runIssue = async () => {
  if (!rgb11WriteAllowed.value) {
    message.value = t('accountManagement.readiness.moduleBlocked')
    success.value = false
    warning.value = false
    return
  }
  const issueAmounts = parsedIssueAmounts.value
  const inflation = parsedInflationAmounts.value
  const issuePrecision = parsedPrecision.value
  const normalizedTicker = ticker.value.trim()
  const normalizedName = name.value.trim()
  const normalizedDetails = details.value.trim()
  submitted.value = true
  const [invalidField] = Object.keys(fieldErrors.value) as IssueField[]
  if (invalidField) {
    message.value = ''
    success.value = false
    warning.value = false
    await nextTick()
    document.getElementById(inputId(invalidField))?.focus()
    return
  }
  loading.value = true
  message.value = ''
  success.value = false
  warning.value = false
  armor.value = ''
  contractConsignmentBase64.value = ''
  issuedSummary.value = null
  try {
    const [err, result] = await walletManager.issueRGB11Asset({
      kind: schema.value,
      ticker: normalizedTicker,
      name: normalizedName,
      details: normalizedDetails,
      precision: issuePrecision,
      amounts: issueAmounts,
      inflation: inflation,
      reject_url: parsedRejectUrl.value,
    })
    if (err || !result) throw err || new Error(t('rgb11Transfer.issueFailed'))
    issuedSummary.value = { ticker: result.ticker, displayName: result.name, contractId: result.contract_id, assetName: result.asset }
    const [exportError, files] = await walletManager.exportRGB11Contract(result.asset)
    if (exportError) throw exportError
    const armored = files?.find(file => file.name.endsWith('.asc'))
    armor.value = armored ? exportFileText(armored) : ''
    contractConsignmentBase64.value = files?.find(file => file.name.endsWith('.rgb'))?.data || ''
    success.value = true
    message.value = t('rgb11Transfer.issued')
    emit('completed')
  } catch (error: any) {
    message.value = displayOperationError(error, 'rgb11Transfer.issueFailed')
  } finally {
    loading.value = false
  }
}

const copyContract = async () => {
  if (!armor.value) return
  await copy(armor.value)
  message.value = t('rgb11Transfer.copied')
  success.value = true
  warning.value = false
}

const downloadContract = () => {
  const data = contractConsignmentBase64.value
  const id = issuedSummary.value?.contractId
  if (!data || !id) return
  try {
    const filename = downloadRGB11ContractFile(id, data)
    message.value = t('common.fileDownloadStarted', { filename })
    success.value = true
    warning.value = false
  } catch (error) {
    message.value = displayOperationError(error, 'rgb11Transfer.exportContractFailed')
    success.value = false
    warning.value = false
  }
}

const copyAssetName = async () => {
  if (!issuedSummary.value?.assetName) return
  await copy(issuedSummary.value.assetName)
  message.value = t('rgb11Transfer.assetNameCopied')
  success.value = true
  warning.value = false
}

watch(schema, (current, previous) => {
  if (previous === 'NIA' && amounts.value === defaultNIASupply) amounts.value = ''
  if (current === 'NIA' && !amounts.value.trim()) amounts.value = defaultNIASupply
})

watch(isOpen, (open) => {
  if (!open) {
    schema.value = 'NIA'
    ticker.value = ''
    name.value = ''
    details.value = ''
    precision.value = '0'
    amounts.value = defaultNIASupply
    inflationAmounts.value = ''
    advancedAllocations.value = false
    submitted.value = false
    rejectListUrl.value = ''
    loading.value = false
    message.value = ''
    success.value = false
    warning.value = false
    armor.value = ''
    contractConsignmentBase64.value = ''
    issuedSummary.value = null
  }
})
</script>
