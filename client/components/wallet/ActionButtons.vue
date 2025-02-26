<template>
  <div class="grid grid-cols-3 gap-4 mb-8">
    <Button
      v-for="action in actions"
      :key="action.name"
      @click="action.onClick"
    >
      <!-- <component :is="action.icon" class="w-6 h-6 mb-2" /> -->
      <span>{{ action.name }}</span>
    </Button>
  </div>
</template>

<script setup lang="ts">
// import { QrCode, Send, History, Bitcoin } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'

const actions = [
  { 
    name: 'Receive', 
    // icon: QrCode,
    onClick: () => emit('receive')
  },
  { 
    name: 'Send', 
    // icon: Send,
    onClick: () => emit('send')
  },
  { 
    name: 'History', 
    // icon: History,
    onClick: () => emit('history')
  }
]

const emit = defineEmits(['receive', 'send', 'history', 'buy'])
</script>