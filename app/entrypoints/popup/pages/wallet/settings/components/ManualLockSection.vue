<template>
  <div class="mb-4 flex gap-2">
    <Input 
      v-model="utxoInput" 
      :placeholder="t('utxoManager.inputPlaceholder')" 
      class="flex-1" 
    />
    <Button 
      :loading="lockLoading" 
      @click="handleLockUtxo" 
      variant="default"
    >
      {{ t('utxoManager.lockBtn') }}
    </Button>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'

const { t } = useI18n()

const utxoInput = ref('')
const lockLoading = ref(false)

const emit = defineEmits<{
  lockUtxo: [utxo: string]
}>()

const handleLockUtxo = async () => {
  if (!utxoInput.value) return
  lockLoading.value = true
  try {
    await emit('lockUtxo', utxoInput.value)
    utxoInput.value = ''
  } finally {
    lockLoading.value = false
  }
}
</script>


