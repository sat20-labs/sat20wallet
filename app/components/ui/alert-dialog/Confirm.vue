<script setup lang="ts">
import { ref, watch } from 'vue'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'

interface ConfirmProps {
  open: boolean
  title?: string
  message: string
  confirmText?: string
  cancelText?: string
  type?: 'warning' | 'info' | 'danger'
}

const props = withDefaults(defineProps<ConfirmProps>(), {
  title: '确认操作',
  confirmText: '确定',
  cancelText: '取消',
  type: 'warning',
})

const emit = defineEmits<{
  'update:open': [value: boolean]
  confirm: []
}>()

const isOpen = ref(props.open)

watch(() => props.open, (newVal) => {
  console.log('Confirm组件: props.open 变化:', newVal)
  isOpen.value = newVal
  console.log('Confirm组件: isOpen.value 设置为:', isOpen.value)
})

const handleConfirm = () => {
  console.log('Confirm组件: handleConfirm 被调用')
  emit('update:open', false)
  console.log('Confirm组件: emit(update:open, false)')
  emit('confirm')
  console.log('Confirm组件: emit(confirm)')
}

const handleCancel = () => {
  emit('update:open', false)
}

// 根据类型获取图标和颜色
const getIconAndColor = (type: string) => {
  switch (type) {
    case 'danger':
      return { icon: 'mdi:alert-circle', colorClass: 'text-red-500', confirmVariant: 'destructive' as const }
    case 'warning':
      return { icon: 'mdi:alert', colorClass: 'text-yellow-500', confirmVariant: 'destructive' as const }
    default:
      return { icon: 'mdi:help-circle', colorClass: 'text-blue-500', confirmVariant: 'default' as const }
  }
}
</script>

<template>
  <Dialog v-model:open="isOpen">
    <DialogContent class="sm:max-w-md">
      <DialogHeader>
        <div class="flex items-center gap-3">
          <Icon
            :icon="getIconAndColor(props.type).icon"
            :class="getIconAndColor(props.type).colorClass"
            class="h-6 w-6"
          />
          <DialogTitle>{{ props.title }}</DialogTitle>
        </div>
      </DialogHeader>

      <div class="py-4">
        <DialogDescription class="text-center">
          {{ props.message }}
        </DialogDescription>
      </div>

      <DialogFooter class="gap-2">
        <Button variant="outline" @click="handleCancel" class="flex-1">
          {{ props.cancelText }}
        </Button>
        <Button :variant="getIconAndColor(props.type).confirmVariant" @click="handleConfirm" class="flex-1">
          {{ props.confirmText }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>