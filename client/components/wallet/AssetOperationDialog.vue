<template>
  <Dialog :open="isOpen" @update:open="isOpen = $event">
    <DialogContent class="w-[330px] rounded-lg bg-zinc-950">
      <DialogHeader class="flex flex-row items-center justify-between">
        <div>
          <DialogTitle>{{ title }}</DialogTitle>
          <DialogDescription>
            <hr class="mb-6 mt-2 border-t-1 border-zinc-900">
            {{ description }}          
          </DialogDescription>
        </div>
        <template v-if="props.operationType === 'send' && props.chain === 'bitcoin'">
          <Button variant="ghost" size="icon" class="ml-2" @click="goSplitAsset" aria-label="Split Asset">
            <Icon icon="mdi:content-cut" class="w-6 h-6 text-primary" />
          </Button>
        </template>
      </DialogHeader>

      <div class="space-y-4">
        <div class="space-y-2">
          <Label>{{ $t('assetOperationDialog.amount') }}</Label>
          <div class="flex items-center gap-2">
            <Input :model-value="amount" type="number" :placeholder="$t('assetOperationDialog.enterAmount')" class="h-12 bg-zinc-800"
              @update:modelValue="handleAmountUpdate" />
            <Button
              variant="outline"
              class="h-12 px-4 text-sm border border-zinc-600 hover:bg-zinc-700"
              @click="setMaxAmount"
            >
              {{ $t('assetOperationDialog.max') }}
            </Button>
          </div>
        </div>
        <div v-if="needsAddress" class="space-y-2">
          <Label>{{ $t('assetOperationDialog.address') }}</Label>
          <Input :model-value="address" type="text" :placeholder="$t('assetOperationDialog.enterAddress')" class="h-12 bg-zinc-800"
            @update:modelValue="handleAddressUpdate" />
        </div>
      </div>
      <DialogFooter>
        <Button class="w-full h-11 mb-2" :disabled="needsAddress && !address" @click="confirmOperation">
          {{ $t('assetOperationDialog.confirm') }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>

  <AlertDialog v-model:open="showAlertDialog">
    <AlertDialogContent class="w-[330px] rounded-lg bg-zinc-900">
      <AlertDialogTitle class="gap-2 flex flex-col items-center">
        <span class="text-lg font-semibold">{{ $t('assetOperationDialog.pleaseConfirm') }}</span>
        <span class="mt-2 w-full"><Separator /></span>
      </AlertDialogTitle>
      <AlertDialogDesc class="flex justify-center">
        <Icon icon="prime:check-circle" class="w-12 h-12 mr-2 text-green-600" />
        {{ $t('assetOperationDialog.confirmOperation') }}
      </AlertDialogDesc>
      <AlertDialogFoot class="my-4 gap-2">
        <AlertDialogCancel @click="showAlertDialog = false">{{ $t('assetOperationDialog.cancel') }}</AlertDialogCancel>
        <AlertDialogAction @click="handleConfirm">{{ $t('assetOperationDialog.confirm') }}</AlertDialogAction>
      </AlertDialogFoot>
    </AlertDialogContent>
  </AlertDialog>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { Separator } from '@/components/ui/separator'
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
import { useRouter } from 'vue-router'
import { Icon } from '@iconify/vue'

interface Props {
  title: string
  description: string
  amount: string
  address: string
  maxAmount?: string // 新增的 prop
  assetType?: string
  assetTicker?: string
  assetKey?: string
  chain?: string
  operationType?: 'send' | 'deposit' | 'withdraw' | 'lock' | 'unlock' | 'splicing_in' | 'splicing_out'
}

const props = withDefaults(defineProps<Props>(), {
  address: '',
  operationType: undefined,
  chain: 'bitcoin'
})

console.log('props', props);

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
  console.log('handleConfirm called'); // 调试日志
  emit('confirm')
  showAlertDialog.value = false
  setTimeout(() => {
    isOpen.value = false
    document.body.removeAttribute('style')
  }, 300)
}

// 设置最大值
const setMaxAmount = () => {
  if (props.maxAmount) {
    emit('update:amount', props.maxAmount) // 将最大值传递给父组件
  }
}

const router = useRouter()

const goSplitAsset = () => {
  router.push(`/wallet/split-asset?assetName=${props.assetKey}`)
}

</script>