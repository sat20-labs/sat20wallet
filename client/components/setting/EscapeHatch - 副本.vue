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
          Your assets are under your control. By broadcasting the commitment transaction, you can safely retrieve your assets from the channel without third-party permission.
        </p>

        <!-- Current Commitment Transaction Info -->
        <div class="mt-6">
          <h3 class="text-md font-bold text-zinc-200">Current Commitment Transaction Info:</h3>

          <!-- Input Section -->
          <div class="mt-4">
            <button
              @click="toggleInputDetails"
              class="flex justify-between items-center w-full text-left text-primary font-medium"
            >
              <span>Input</span>
              <span>{{ showInputDetails ? '▲' : '▼' }}</span>
            </button>
            <div v-if="!showInputDetails" class="mt-2 text-sm text-muted-foreground">
              Total Input: 0.023 BTC
            </div>
            <div v-if="showInputDetails" class="mt-2 space-y-2">
              <div class="flex justify-between text-sm text-muted-foreground">
                <span>utxo1</span>
                <span>0.015 BTC</span>
              </div>
              <div class="flex justify-between text-sm text-muted-foreground">
                <span>RarePizza × 100</span>
                <span>bc1q...</span>
              </div>
              <div class="flex justify-between text-sm text-muted-foreground">
                <span>utxo2</span>
                <span>0.008 BTC</span>
              </div>
              <div class="flex justify-between text-sm text-muted-foreground">
                <span>SAT20-ABC × 1</span>
                <span>bc1p...</span>
              </div>
            </div>
          </div>

          <!-- Output Section -->
          <div class="mt-4">
            <button
              @click="toggleOutputDetails"
              class="flex justify-between items-center w-full text-left text-primary font-medium"
            >
              <span>Output</span>
              <span>{{ showOutputDetails ? '▲' : '▼' }}</span>
            </button>
            <div v-if="!showOutputDetails" class="mt-2 text-sm text-muted-foreground">
              Total Output: 0.05 BTC
            </div>
            <div v-if="showOutputDetails" class="mt-2 space-y-2">
              <div class="flex justify-between text-sm text-muted-foreground">
                <span>utxoX</span>
                <span>0.02 BTC</span>
              </div>
              <div class="flex justify-between text-sm text-muted-foreground">
                <span>SAT20-ABC × 1</span>
                <span>Belongs to Counterparty</span>
              </div>
              <div class="flex justify-between text-sm text-muted-foreground">
                <span>utxoY</span>
                <span>0.03 BTC</span>
              </div>
              <div class="flex justify-between text-sm text-muted-foreground">
                <span>Belongs to Counterparty</span>
                <span>Counterparty Address</span>
              </div>
            </div>
          </div>

           <!-- Your Assets -->
          <div class="mt-4">
            <h4 class="text-md  text-primary font-medium">Your Assets in This Channel</h4>
            <div class="mt-2 space-y-2 text-sm text-muted-foreground">
              <div class="flex justify-between">
                <span>RarePizza × 100</span>
                <span>SAT20-ABC × 1</span>
              </div>
            </div>
          </div>
        </div>
      </div>
   

      <!-- Escape Options -->
      <div class="border-t border-zinc-900/30">
        <h3 class="text-md font-bold text-zinc-200 mt-2">Escape Options</h3>
        <div class="space-y-2 mt-2">
          <Button variant="secondary" @click="showCommitmentDetails = true" class="w-full text-white">View Commitment
            TX</Button>
          <!-- Commitment Details Modal -->
          <div v-if="showCommitmentDetails"
            class="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50">
            <div class="bg-zinc-800 rounded-lg w-[95%] max-w-lg max-h-[90vh] overflow-y-auto custom-scrollbar p-4">
              <CommitmentDetails @close="showCommitmentDetails = false" />
            </div>
          </div>

          <div class="flex space-x-2">
           
              <Popover v-if="channel" class="flex-1">
                <PopoverTrigger asChild>
                  <Button class="w-full bg-green-600" :disabled="channel.status !== 16">
                    <Icon icon="material-symbols:flash-off" class="mr-2 h-4 w-4" />
                    Close
                  </Button>
                </PopoverTrigger>
                <PopoverContent>
                  <div class="p-4 space-y-4">
                    <p>Are you sure you want to close this channel?</p>
                    <div class="flex justify-end gap-2">
                      <Button variant="ghost" @click="() => { }"> Cancel </Button>
                      <Button :class="{ 'opacity-50 pointer-events-none': loading }" variant="destructive"
                        @click="closeChannel">
                        Confirm
                      </Button>
                    </div>
                  </div>
                </PopoverContent>
              </Popover>
           
          </div>
        </div>
        <!-- Recovery Safety Tips -->
      <div>
        <button
          @click="showSafetyTips = !showSafetyTips"
          class="flex items-center justify-between w-full text-left text-yellow-500 font-medium"
        >
          ⚠️ Recovery Safety Tips
          <span>{{ showSafetyTips ? '▲' : '▼' }}</span>
        </button>
        <div v-if="showSafetyTips" class="mt-2 p-2 bg-yellow-100 rounded-lg text-sm text-yellow-900">
          <ul class="list-disc pl-4">
            <li>Force close will lock your funds for a certain time period (usually 24 blocks).</li>
            <li>Make sure your node stays online to monitor the broadcast and sweep TX.</li>
            <li>
              If you have issues recovering funds, please refer to our
              <a href="/recovery-guide" class="text-blue-500 underline">Recovery Guide</a>.
            </li>
          </ul>
        </div>
      </div>
        <p class="text-xs text-muted-foreground my-4">
          <Icon icon="mdi:information-outline" class="inline-block mr-1" />
          Funds can always be safely reclaimed.
        </p>
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
  background-color: rgba(255, 255, 255, 0.2);
  border-radius: 4px;
}

.custom-scrollbar::-webkit-scrollbar-thumb:hover {
  background-color: rgba(255, 255, 255, 0.4);
}

.custom-scrollbar::-webkit-scrollbar-track {
  background-color: transparent;
}
</style>