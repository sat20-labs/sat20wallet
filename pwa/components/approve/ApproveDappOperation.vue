<template>
  <LayoutApprove @confirm="confirm" @cancel="cancel">
    <div class="space-y-3 p-4 text-sm">
      <p class="font-medium">{{ description }}</p>
      <div class="rounded-md border border-border bg-muted/30 p-3 text-xs break-all">
        <p><span class="text-muted-foreground">DApp:</span> {{ metadata.origin }}</p>
        <p v-if="data.utxo"><span class="text-muted-foreground">UTXO:</span> {{ data.utxo }}</p>
        <p v-if="data.reason"><span class="text-muted-foreground">Reason:</span> {{ data.reason }}</p>
      </div>
      <p class="text-xs text-destructive">Only approve if you recognize this DApp and operation.</p>
    </div>
  </LayoutApprove>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import LayoutApprove from '@/components/layout/LayoutApprove.vue'
import { Message } from '@/types/message'

const props = defineProps<{ data: Record<string, unknown>; metadata: Record<string, unknown> }>()
const emit = defineEmits(['confirm', 'cancel'])
const description = computed(() => {
  switch (props.metadata.action) {
    case Message.MessageAction.LOCK_UTXO:
    case Message.MessageAction.LOCK_UTXO_SATSNET:
      return 'Allow this DApp to lock the selected UTXO?'
    case Message.MessageAction.UNLOCK_UTXO:
    case Message.MessageAction.UNLOCK_UTXO_SATSNET:
      return 'Allow this DApp to unlock its selected UTXO?'
    default:
      return 'Allow this DApp to broadcast this transaction?'
  }
})
const confirm = () => emit('confirm', true)
const cancel = () => emit('cancel')
</script>
