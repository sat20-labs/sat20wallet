<template>
  <Dialog :open="isOpen" @update:open="isOpen = $event">
    <DialogContent class="max-h-[90vh] w-[380px] overflow-y-auto rounded-lg bg-black">
      <DialogHeader>
        <DialogTitle>{{ $t('rgb11Transfer.importTitle') }}</DialogTitle>
        <DialogDescription>{{ $t('rgb11Transfer.importDescription') }}</DialogDescription>
      </DialogHeader>
      <div class="space-y-3">
        <Textarea v-model="consignment" spellcheck="false" class="min-h-56 bg-zinc-900 font-mono text-xs" />
        <p v-if="message" class="break-all text-xs"
          :class="warning ? 'text-amber-500' : success ? 'text-emerald-400' : 'text-red-400'">
          {{ message }}
        </p>
        <Button class="w-full" :disabled="loading || !consignment.trim()" @click="runImport">
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
const loading = ref(false)
const message = ref('')
const success = ref(false)
const warning = ref(false)
const { t } = useI18n()

const runImport = async () => {
  loading.value = true
  message.value = ''
  success.value = false
  warning.value = false
  const [err, result] = await walletManager.importRGB11Contract(consignment.value.trim())
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
    loading.value = false
    message.value = ''
    success.value = false
    warning.value = false
  }
})
</script>
