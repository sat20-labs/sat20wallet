<template>
  <Dialog :open="isOpen" @update:open="isOpen = $event">
    <DialogContent class="sm:max-w-md">
      <DialogHeader>
        <DialogTitle>{{ type === 'deposit' ? 'Deposit' : 'Withdraw' }} {{ asset?.ticker || asset?.label }}</DialogTitle>
        <DialogDescription>
          Review pool information before {{ type === 'deposit' ? 'depositing' : 'withdrawing' }}
        </DialogDescription>
      </DialogHeader>
      <div class="space-y-4">
        <div class="space-y-2">
          <Label>Assets Information</Label>
          <div class="rounded-lg border p-3">
            <div class="text-sm">
              <div>Asset: {{ asset?.ticker || asset?.label }}</div>
              <div>Balance: {{ asset?.amount }}</div>
            </div>
          </div>
        </div>
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
              {{ asset?.ticker || asset?.label }}
            </span>
          </div>
        </div>
      </div>
      <DialogFooter>
        <Button @click="handleOperation" class="w-full">{{ type === 'deposit' ? 'Deposit' : 'Withdraw' }}</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>

<script setup lang="ts">
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'

interface Props {
  type: 'deposit' | 'withdraw'
  asset: any
  amount: string
}

const props = defineProps<Props>()
const isOpen = defineModel('open', { type: Boolean })

const emit = defineEmits<{
  'update:amount': [value: string]
  'confirm': [type: string, asset: any, amount: string]
}>()

const handleAmountUpdate = (value: string | number) => {
  emit('update:amount', value.toString())
}

const handleOperation = () => {
  emit('confirm', props.type, props.asset, props.amount)
  isOpen.value = false
}
</script> 