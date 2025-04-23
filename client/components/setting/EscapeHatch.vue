<template>
  <div class="w-full px-2  bg-zinc-700/40 rounded-lg">
    <button @click="isExpanded = !isExpanded"
      class="flex items-center justify-between w-full p-2 text-left text-primary font-medium rounded-lg">
      <div>
        <h2 class="text-lg font-bold text-zinc-200">Escape Hatch Options</h2>
        <p class="text-muted-foreground">Channel Info & Secure Exit from Channel</p>
      </div>
      <div class="mr-2">
        <Icon v-if="isExpanded" icon="lucide:chevrons-up" class="mr-2 h-4 w-4" />
        <Icon v-else icon="lucide:chevrons-down" class="mr-2 h-4 w-4" />
      </div>
    </button>
    <div v-if="isExpanded" class="space-y-6 py-2 px-2 mb-4">
      <div class="w-full px-4 py-4 bg-zinc-700/40 rounded-lg">
    <h2 class="text-lg font-bold text-zinc-200">Asset Safety</h2>
    <p class="text-sm text-muted-foreground mt-2">
      Your assets are safe. They are secured by your commitment transaction. By broadcasting the commitment transaction, you can reclaim your funds at any time without third-party permission.
    </p>

    <!-- Broadcast Button -->
    <div class="mt-6">
      <Button class="w-full bg-purple-600 text-white">BROADCAST TX</Button>
    </div>

    <!-- Current Commitment Transaction -->
    <div class="mt-6">
      <h3 class="text-md font-bold text-zinc-200">Current Commitment Transaction</h3>

       <!-- Your Assets Section -->
       <div class="mt-6">
        <h4 class="text-sm font-bold text-zinc-200">Your Assets in This Channel</h4>
        <div class="overflow-x-auto custom-scrollbar">
          <div class="min-w-max grid grid-cols-4 gap-4 text-sm text-muted-foreground mt-2 whitespace-nowrap">

          <div class="flex flex-col">
            <span class="font-medium">Asset</span>
            <span>RarePizza</span>
            <span>SAT20-ABC</span>
          </div>
          <div class="flex flex-col">
            <span class="font-medium">Amount</span>
            <span>100</span>
            <span>1</span>
          </div>
          </div>
        </div>
      </div>

      <!-- Inputs Section -->
      <div class="mt-4">
        <h4 class="text-sm font-bold text-zinc-200">Inputs</h4>
        <div class="overflow-x-auto custom-scrollbar">
          <div class="min-w-max grid grid-cols-4 gap-4 text-sm text-muted-foreground mt-2 whitespace-nowrap">

          <div class="flex flex-col">
            <span class="font-medium">UTXO</span>
            <span>utxo0</span>
            <span>utxo1</span>
          </div>
          <div class="flex flex-col">
            <span class="font-medium">Value</span>
            <span>0.015 BTC</span>
            <span>0.02 BTC</span>
          </div>
          <div class="flex flex-col">
            <span class="font-medium">Assets</span>
            <span>RarePizza × 100</span>
            <span>-</span>
          </div>
          <div class="flex flex-col">
            <span class="font-medium">Address</span>
            <span>bc1q...</span>
            <span>bc1q...</span>
          </div>
        </div>
        </div>
      </div>

      <!-- Outputs Section -->
      <div class="mt-6">
        <h4 class="text-sm font-bold text-zinc-200">Outputs</h4>
        <div class="overflow-x-auto custom-scrollbar">
          <div class="min-w-max grid grid-cols-4 gap-4 text-sm text-muted-foreground mt-2 whitespace-nowrap">

          <div class="flex flex-col border-b border-zinc-600/30">
            <span class="font-medium">UTXO</span>
            <span>utxo0</span>
            <span>utxo1</span>
          </div>
          <div class="flex flex-col border-b border-zinc-600/30">
            <span class="font-medium">Value</span>
            <span>0.003 BTC</span>
            <span>0.02 BTC</span>
          </div>
          <div class="flex flex-col border-b border-zinc-600/30">
            <span class="font-medium">Assets</span>
            <span>SAT20-ABC × 1</span>
            <span>-</span>
          </div>
          <div class="flex flex-col border-b border-zinc-600/30">
            <span class="font-medium">Address</span>
            <span>Your address</span>
            <span>bc1q...</span>
          </div>
          </div>
        </div>
      </div>

     

    </div>
  </div>
    </div>
  </div>
</template>


<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { storeToRefs } from 'pinia'
import { Button } from '@/components/ui/button'
import Progress from '@/components/ui/progress/index.vue'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Icon } from '@iconify/vue'
import { useChannelStore } from '@/store'
import satsnetStp from '@/utils/stp'
import { useToast } from '@/components/ui/toast'
import { hideAddress } from '~/utils'
import { getChannelStatusText } from '~/composables'
import CopyCard from '@/components/common/CopyCard.vue'
import { sleep } from 'radash'

import { useGlobalStore, type Env } from '@/store/global'

import CommitmentDetails from './CommitmentDetails.vue'

const loading = ref(false)
const showSafetyTips = ref(false)
const isExpanded = ref(false)
const showCommitmentDetails = ref(false)

const showInputDetails = ref(false)
const showOutputDetails = ref(false)

const toggleInputDetails = () => {
  showInputDetails.value = !showInputDetails.value
}

const toggleOutputDetails = () => {
  showOutputDetails.value = !showOutputDetails.value
}

const globalStore = useGlobalStore()

const computedEnv = computed<Env>({
  get: () => globalStore.env,
  set: (newValue) => {
    globalStore.setEnv(newValue)
    window.location.reload()
  }
})

const channelStore = useChannelStore()
const { channel, plainList, sat20List, brc20List, runesList } = storeToRefs(channelStore)
const { toast } = useToast()

const checkChannel = async () => {
  const chanid = channel.value!.chanid

  const [err, result] = await satsnetStp.getChannelStatus(chanid)
  console.log(result)
  if (err) {
    return false
  }
  if (result >= 16) {
    return true
  }
  if (result !== 16) {
    return false
  }
  return true
}

const closeChannel = async (closeHanlder: any) => {
  loading.value = true
  console.log('close')

  const chanid = channel.value!.chanid
  const status = await checkChannel()
  if (!status) {
    toast({
      title: 'Error',
      description: 'Channel tx has not been confirmed',
      variant: 'destructive',
    })
    loading.value = false
    return
  }
  const [err] = await satsnetStp.closeChannel(chanid, 0, false)
  closeHanlder()
  if (err) {
    const [errForce] = await satsnetStp.closeChannel(chanid, 0, true)
    closeHanlder()
    if (errForce) {
      toast({
        title: 'Error',
        description: 'Force close failed',
        variant: 'destructive',
      })
    }
    loading.value = false
    return
  } else {
    toast({
      title: 'Success',
      description: 'Close success',
    })
  }
  loading.value = false
  await channelStore.getAllChannels()
}
onMounted(() => {
  channelStore.getAllChannels()
})
</script>

<style scoped>
/* 自定义滚动条样式 */
.custom-scrollbar::-webkit-scrollbar {
  width: 8px;
  background-color: transparent;
}

.custom-scrollbar::-webkit-scrollbar-thumb {
  background-color: rgba(255, 255, 255, 0.03);
  height: 4px;
  border-radius: 4px;
}

.custom-scrollbar::-webkit-scrollbar-thumb:hover {
  background-color: rgba(255, 255, 255, 0.219);
}

.custom-scrollbar::-webkit-scrollbar-track {
  height: 4px;
  background-color: transparent;
}
</style>