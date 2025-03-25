<template>
  <Dialog :open="isOpen" @update:open="isOpen = $event">
    <DialogContent class="sm:max-w-md">
      <DialogHeader>
        <DialogTitle>{{ title }}</DialogTitle>
        <DialogDescription>
          {{ description }}
        </DialogDescription>
      </DialogHeader>
      <div class="space-y-4">
        <div class="space-y-2">
          <Label>Amount</Label>
          <div class="flex items-center gap-2">
            <Input
              :model-value="amount"
              type="number"
              placeholder="Enter amount"
              @update:modelValue="handleAmountUpdate"
            />
            <span class="text-sm text-muted-foreground">
              {{ assetUnit }}
            </span>
          </div>
        </div>
      </div>
      <DialogFooter>
        <Button @click="handleOperation" class="w-full">Confirm</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'

interface Props {
  title: string
  description: string
  amount: string
  assetType?: string
  assetTicker?: string
}

const props = defineProps<Props>()
const isOpen = defineModel('open', { type: Boolean })

const assetUnit = computed(() => {
  if (props.assetType === 'BTC') {
    return 'sats'
  }
  return props.assetTicker || 'sats'
})

const emit = defineEmits<{
  'update:amount': [value: string]
  'confirm': []
}>()

const handleAmountUpdate = (value: string | number) => {
  emit('update:amount', value.toString())
}

const handleOperation = () => {
  emit('confirm')
  isOpen.value = false
}
</script>