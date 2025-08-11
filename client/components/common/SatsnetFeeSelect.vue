<template>
  <div>
    <Button
      @click="isModalOpen = true"
      variant="outline"
      size="xs"
      class="rounded-md text-gray-300 text-xs font-normal ml-2"
    >
      <Icon icon="lucide:fuel" class="w-1 h-1 mr-[0.5px] text-gray-500" />
      <span class="mr-2">{{ displayedRate }}</span>
    </Button>

    <Dialog v-model:open="isModalOpen">
      <DialogContent class="sm:max-w-[425px]">
        <DialogHeader>
          <DialogTitle>{{ $t('btcFeeSelect.selectFeeRate') }}</DialogTitle>
        </DialogHeader>
        <div class="grid grid-cols-4 gap-2">
          <Button
            v-for="option in options"
            :key="option.key"
            @click="selectRate(option.key)"
            :variant="selectedRate === option.key ? 'default' : 'outline'"
            class="h-auto flex flex-col items-center justify-center p-2 text-xs"
          >
            <span class="text-lg mb-1">{{ option.icon }}</span>
            <span>{{ $t(`btcFeeSelect.${option.label}`) }}</span>
            <span class="text-[10px]">{{ option.value }} sats/Tx</span>
          </Button>
        </div>
        <div v-if="selectedRate === 'custom'" class="mt-4 space-y-2">
          <Label for="customRate" class="text-sm font-medium">
            {{ $t('btcFeeSelect.customFeeRate') }}
          </Label>
          <div class="flex items-center gap-2">
            <Input
              id="customRate"
              v-model.lazy.number="customRate"
              type="number"
              class="w-24"
              :min="1"
              :max="1000"
              step="1"
              inputmode="numeric"
              :placeholder="$t('btcFeeSelect.enterRate')"
              @keydown="onKeydownInteger"
              @change="onChangeClampInteger"
            />
            <!-- <Slider v-model="customRate" :min="1" :max="1000" class="flex-1" /> -->
          </div>
        </div>
        <DialogFooter>
          <Button @click="isModalOpen = false" class="w-full">
            {{ $t('btcFeeSelect.confirm') }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>

<script setup lang="ts">
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { watchOnce } from '@vueuse/core'
import { useWalletStore } from '@/store'

const walletStore = useWalletStore()
const emit = defineEmits(['change'])

const feeData = {
  fastestFee: 20,
  halfHourFee: 10,
  hourFee: 10,
}



const options = computed(() => [
  {
    icon: '🐢',
    key: 'slow',
    value: feeData?.hourFee || 0,
    label: 'slow',
  },
  {
    icon: '🚗',
    key: 'average',
    value: feeData?.halfHourFee || 0,
    label: 'average',
  },
  {
    icon: '🚀',
    key: 'fast',
    value: feeData?.fastestFee || 0,
    label: 'fast',
  },
  {
    icon: '⚙️',
    key: 'custom',
    value: feeData?.fastestFee || 0,
    label: 'custom',
  },
])

const selectedRate = ref('average')
const customRate = ref(
  options.value.find((option) => option.key === 'fast')!.value
)
const isModalOpen = ref(false)

watchOnce(options, () => {
  customRate.value = options.value.find(
    (option) => option.key === 'fast'
  )!.value
})

const selectRate = (k: string) => {
  selectedRate.value = k
  if (k !== 'custom') {
    customRate.value = options.value.find((option) => option.key === k)!.value
    isModalOpen.value = false
  }
}

const displayedRate = computed(() =>
  selectedRate.value === 'custom'
    ? customRate.value
    : options.value.find((option) => option.key === selectedRate.value)!.value
)

const onKeydownInteger = (e: KeyboardEvent) => {
  const blockedKeys = ['.', 'e', 'E', '+', '-']
  if (blockedKeys.includes(e.key)) {
    e.preventDefault()
  }
}

const onChangeClampInteger = (e: Event) => {
  const input = e.target as HTMLInputElement
  if (input.value === '') {
    customRate.value = 1
    input.value = '1'
    return
  }
  const parsed = parseInt(input.value, 10)
  const clamped = Number.isNaN(parsed)
    ? 1
    : Math.min(1000, Math.max(1, Math.trunc(parsed)))
  if (String(clamped) !== input.value) input.value = String(clamped)
  if (clamped !== customRate.value) customRate.value = clamped
}

watch(
  displayedRate,
  (value) => {
    emit('change', value)
  },
  { immediate: true }
)
</script>
