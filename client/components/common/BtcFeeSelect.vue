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
            <span class="text-[10px]">{{ option.value }} sats/vB</span>
          </Button>
        </div>
        <div v-if="selectedRate === 'custom'" class="mt-4 space-y-2">
          <Label for="customRate" class="text-sm font-medium">
            {{ $t('btcFeeSelect.customFeeRate') }}
          </Label>
          <div class="flex items-center gap-2">
            <Input
              id="customRate"
              v-model="customRate"
              type="number"
              class="w-24"
              :min="1"
              :max="1000"
              :placeholder="$t('btcFeeSelect.enterRate')"
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
// import { getBtcFee } from '~layer/apis';
import { Network } from '@/types'
import { ordxApi } from '@/apis'
import { useQuery } from '@tanstack/vue-query'
import { useWalletStore } from '@/store'

const walletStore = useWalletStore()
const { network } = storeToRefs(walletStore)
const emit = defineEmits(['change'])

const defaultData = {
  fastestFee: 1,
  halfHourFee: 1,
  hourFee: 1,
}
const {
  data: res,
} = useQuery({
  queryKey: ['btcFee', network],
  queryFn: ({ queryKey }) => ordxApi.getRecommendedFees({ network: queryKey[1] }),
  refetchInterval: 1000 * 60 * 3,
})

const feeData = computed(() => res.value?.data || defaultData)

const options = computed(() => [
  {
    icon: '🐢',
    key: 'slow',
    value: feeData.value?.hourFee || 0,
    label: 'slow',
  },
  {
    icon: '🚗',
    key: 'average',
    value: feeData.value?.halfHourFee || 0,
    label: 'average',
  },
  {
    icon: '🚀',
    key: 'fast',
    value: feeData.value?.fastestFee || 0,
    label: 'fast',
  },
  {
    icon: '⚙️',
    key: 'custom',
    value: feeData.value?.fastestFee || 0,
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

watch(
  displayedRate,
  (value) => {
    emit('change', value)
  },
  { immediate: true }
)
</script>
