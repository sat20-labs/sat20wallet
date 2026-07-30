<template>
  <Dialog :open="isOpen" @update:open="isOpen = $event">
    <DialogContent class="max-h-[90vh] w-[380px] overflow-y-auto rounded-lg bg-black">
      <DialogHeader>
        <DialogTitle>{{ $t('rgb11Transfer.importTitle') }}</DialogTitle>
        <DialogDescription>{{ $t('rgb11Transfer.importDescription') }}</DialogDescription>
      </DialogHeader>
      <div class="space-y-3">
        <Textarea v-model="consignment" spellcheck="false" class="min-h-56 bg-zinc-900 font-mono text-xs"
          :placeholder="$t('rgb11Transfer.importArmorPlaceholder')" @input="selectedFile = null" />
        <div class="space-y-1">
          <input id="rgb11-contract-file" type="file" accept=".rgb,.rgbc,application/octet-stream"
            class="sr-only" @change="selectFile" />
          <label for="rgb11-contract-file"
            class="flex h-10 cursor-pointer items-center justify-center rounded-md border border-zinc-700 bg-zinc-900 px-4 text-sm hover:bg-zinc-800">
            {{ $t('rgb11Transfer.selectContractFile') }}
          </label>
          <p v-if="selectedFile" class="break-all text-xs text-zinc-400">
            {{ $t('rgb11Transfer.selectedContractFile', { name: selectedFile.name }) }}
          </p>
        </div>
        <p v-if="message" class="break-all text-xs"
          :class="warning ? 'text-amber-500' : success ? 'text-emerald-400' : 'text-red-400'">
          {{ message }}
        </p>
        <Button class="w-full" :disabled="loading || (!consignment.trim() && !selectedFile)" @click="runImport">
          {{ loading ? $t('rgb11Transfer.importing') : $t('rgb11Transfer.import') }}
        </Button>
      </div>
    </DialogContent>
  </Dialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import walletManager from '@/utils/sat20'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'

const emit = defineEmits<{ (e: 'completed'): void }>()
const isOpen = defineModel('open', { type: Boolean })
const consignment = ref('')
const selectedFile = ref<File | null>(null)
const loading = ref(false)
const message = ref('')
const success = ref(false)
const warning = ref(false)
const { t } = useI18n()

const selectFile = (event: Event) => {
  const input = event.target as HTMLInputElement
  selectedFile.value = input.files?.[0] || null
  if (selectedFile.value) consignment.value = ''
}

const encodeBase64 = (raw: ArrayBuffer) => {
  const bytes = new Uint8Array(raw)
  let binary = ''
  const chunkSize = 0x8000
  for (let offset = 0; offset < bytes.length; offset += chunkSize) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + chunkSize))
  }
  return btoa(binary)
}

const runImport = async () => {
  loading.value = true
  message.value = ''
  success.value = false
  warning.value = false
  const [err, result] = selectedFile.value
    ? await walletManager.importRGB11ContractFile(encodeBase64(await selectedFile.value.arrayBuffer()))
    : await walletManager.importRGB11Contract(consignment.value.trim())
  if (err || !result?.result) {
    loading.value = false
    message.value = err?.message || t('rgb11Transfer.importFailed')
    return
  }
  const imported = JSON.parse(result.result)
  loading.value = false
  success.value = true
  warning.value = false
  message.value = t('rgb11Transfer.imported', { count: imported.projected || 0 })
  emit('completed')
}

watch(isOpen, (open) => {
  if (!open) {
    consignment.value = ''
    selectedFile.value = null
    loading.value = false
    message.value = ''
    success.value = false
    warning.value = false
  }
})
</script>
