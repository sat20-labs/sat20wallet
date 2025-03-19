<template>
  <div class="text-center mb-2">
    <div class="flex items-center justify-center gap-2 text-gray-400">
      <h3>TOTAL BALANCE</h3>
      <Button
        variant="ghost"
        size="icon"
        class="h-6 w-6"
        @click="toggleBalance"
        :disabled="loading"
      >
        <Icon
          :icon="isHidden ? 'lucide:eye' : 'lucide:eye-off'"
          class="h-4 w-4"
        />
      </Button>
    </div>
    <div class="text-4xl font-mono">
      <template v-if="loading">
        <div class="flex justify-center items-center">
          <Icon icon="lucide:loader-2" class="h-6 w-6 animate-spin" />
        </div>
      </template>
      <template v-else>
        {{ isHidden ? '******' : formattedBalance }}
      </template>
    </div>
    <div class="text-base">{{ currency }}</div>
  </div>
</template>

<script setup lang="ts">
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
  loading: {
    type: Boolean,
    default: false,
  },
})

const isHidden = ref(false)
const formattedBalance = computed(() => props.balance)

const toggleBalance = () => {
  isHidden.value = !isHidden.value
}
</script>
