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
            <Input :model-value="amount" type="number" placeholder="Enter amount"
              @update:modelValue="handleAmountUpdate" />
            <!-- <span class="text-sm text-muted-foreground">
              {{ assetUnit }}
            </span> -->
          </div>
        </div>
        <div v-if="needsAddress" class="space-y-2">
          <Label>Address</Label>
          <Input :model-value="address" type="text" placeholder="Enter address"
            @update:modelValue="handleAddressUpdate" />
        </div>
      </div>
      <DialogFooter>
        <Button class="w-full" :disabled="needsAddress && !address" @click="confirmOperation">Confirm</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>

  <AlertDialog v-model:open="showAlertDialog">
    <AlertDialogContent>
      <AlertDialogTitle>Please Confirm</AlertDialogTitle>
      <AlertDialogDesc>Are you sure you want to proceed with this operation?</AlertDialogDesc>
      <AlertDialogFoot>
        <AlertDialogCancel @click="showAlertDialog = false">Cancel</AlertDialogCancel>
        <AlertDialogAction @click="handleConfirm">Confirm</AlertDialogAction>
      </AlertDialogFoot>
    </AlertDialogContent>
  </AlertDialog>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogTitle,
  AlertDialogDescription as AlertDialogDesc,
  AlertDialogFooter as AlertDialogFoot,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogTrigger
} from '@/components/ui/alert-dialog'

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

const showAlertDialog = ref(false)

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

const confirmOperation = () => {
  showAlertDialog.value = true
}

const handleConfirm = () => {
  emit('confirm')
  showAlertDialog.value = false
  setTimeout(() => {
    isOpen.value = false
  }, 300)
}

</script>