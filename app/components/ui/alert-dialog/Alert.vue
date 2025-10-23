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

interface AlertProps {
  open: boolean
  title?: string
  message: string
  confirmText?: string
  type?: 'info' | 'warning' | 'error' | 'success'
}

const props = withDefaults(defineProps<AlertProps>(), {
  title: '提示',
  confirmText: '确定',
  type: 'info',
})

const emit = defineEmits<{
  'update:open': [value: boolean]
}>()

const isOpen = ref(props.open)

watch(() => props.open, (newVal) => {
  isOpen.value = newVal
})

const handleConfirm = () => {
  emit('update:open', false)
}

const handleClose = () => {
  emit('update:open', false)
}

// 根据类型获取图标和颜色
const getIconAndColor = (type: string) => {
  switch (type) {
    case 'success':
      return { icon: 'mdi:check-circle', colorClass: 'text-green-500' }
    case 'warning':
      return { icon: 'mdi:alert', colorClass: 'text-yellow-500' }
    case 'error':
      return { icon: 'mdi:close-circle', colorClass: 'text-red-500' }
    default:
      return { icon: 'mdi:information', colorClass: 'text-blue-500' }
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

      <DialogFooter>
        <Button @click="handleConfirm" class="w-full">
          {{ props.confirmText }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>