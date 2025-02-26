<template>
  <div class="text-center mb-8">
    <div class="flex items-center justify-center gap-2 text-gray-400 mb-2">
      <h3>TOTAL BALANCE</h3>
      <Button
        variant="ghost"
        size="icon"
        class="h-6 w-6"
        @click="toggleBalance"
      >
        <Icon
          :icon="isHidden ? 'lucide:eye' : 'lucide:eye-off'"
          class="h-4 w-4"
        />
      </Button>
    </div>
    <div class="text-4xl font-mono mb-2">
      {{ isHidden ? '******' : formattedBalance }}
    </div>
    <div class="text-base">{{ currency }}</div>
  </div>
</template>

<script setup>
// import { Eye, EyeOff } from 'lucide-vue-next'
import { Icon } from '@iconify/vue'
import { Button } from '@/components/ui/button'

const props = defineProps({
  balance: {
    type: String,
    default: '0.00142709',
  },
  currency: {
    type: String,
    default: 'BTC',
  },
})

const isHidden = ref(false)
const formattedBalance = computed(() => props.balance)

const toggleBalance = () => {
  isHidden.value = !isHidden.value
}
</script>
