<template>
  <Dialog :open="isOpen" @update:open="isOpen = $event">
    <DialogContent class="sm:max-w-md">
      <DialogHeader>
        <DialogTitle>{{ title }}</DialogTitle>        
        <DialogDescription>
          <hr class="mb-6 mt-1 border-t-1 border-accent">
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
            <!-- <span class="text-sm text-muted-foreground">
              {{ assetUnit }}
            </span> -->
          </div>
        </div>
        <div v-if="needsAddress" class="space-y-2">
          <Label>Address</Label>
          <Input
            :model-value="address"
            type="text"
            placeholder="Enter address"
            @update:modelValue="handleAddressUpdate"
          />
        </div>
      </div>
      <DialogFooter>
        <Button 
          @click="handleOperation" 
          class="w-full"
          :disabled="needsAddress && !address"
        >
          Confirm
        </Button>
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
  address: string
  assetType?: string
  assetTicker?: string
  operationType?: 'send' | 'deposit' | 'withdraw' | 'lock' | 'unlock' | 'splicing_in' | 'splicing_out'
}

const props = withDefaults(defineProps<Props>(), {
  address: '',
  operationType: undefined
})

const isOpen = defineModel('open', { type: Boolean })

const assetUnit = computed(() => {
  if (props.assetType === 'BTC') {
    return 'sats'
  }
  return props.assetTicker || 'sats'
})

const needsAddress = computed(() => {
  return props.operationType === 'send'
})

const emit = defineEmits<{
  'update:amount': [value: string]
  'update:address': [value: string]
  'confirm': []
}>()

const handleAmountUpdate = (value: string | number) => {
  emit('update:amount', value.toString())
}

const handleAddressUpdate = (value: string | number) => {
  emit('update:address', value.toString())
}

const handleOperation = () => {
  if (needsAddress.value && !props.address) {
    return
  }
  emit('confirm')
  isOpen.value = false
}
</script>