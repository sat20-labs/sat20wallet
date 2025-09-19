<template>
  <div class="mb-4">
    <div class="flex justify-between items-center mb-2">
      <span class="text-sm text-zinc-400">{{ t('utxoManager.ordinalsDescription') }}</span>
      <Button 
        :loading="unlockLoading" 
        @click="handleUnlockSelected" 
        variant="default"
        :disabled="selectedCount === 0"
      >
        {{ t('utxoManager.unlockSelectedBtn') }} ({{ selectedCount }})
      </Button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { Button } from '@/components/ui/button'

const { t } = useI18n()

interface Props {
  selectedCount: number
  unlockLoading: boolean
}

defineProps<Props>()

const emit = defineEmits<{
  unlockSelected: []
}>()

const handleUnlockSelected = () => {
  emit('unlockSelected')
}
</script>
