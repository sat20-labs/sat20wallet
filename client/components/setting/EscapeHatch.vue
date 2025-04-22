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
      <!-- Channel Summary -->
      <div class="border-t border-zinc-900/30">
        <h3 class="text-md font-bold text-zinc-200 mt-2">Channel Summary</h3>
        <div class="text-sm text-muted-foreground">
          <div class="flex justify-between">
            <span>Status</span>
            <span class="text-green-500 rounded-lg px-2">Active</span>
          </div>
          <div class="flex justify-between">
            <span>Capacity</span>
            <span class="bg-zinc-900 rounded-xl px-2 py-[2px]">abcd...wxyz1234</span>
          </div>
          <div class="flex justify-between">
            <span>Balance</span>
            <span>650,000 sats</span>
          </div>
          <div class="flex justify-between">
            <span>Estimated total</span>
            <span>70,000 sats</span>
          </div>
        </div>
      </div>

      <!-- Asset Breakdown -->
      <div class="border-t border-zinc-900/30">
        <h3 class="text-md font-bold text-zinc-200 mt-2">Asset Breakdown</h3>
        <div class="text-sm text-muted-foreground">
          <div class="flex justify-between">
            <span>Withdrawable amount</span>
            <span>650,000 sats</span>
          </div>
          <div class="flex justify-between">
            <span>In Channel</span>
            <span>50,000 sats</span>
          </div>
          <div class="flex justify-between">
            <span>Estimated total</span>
            <span>700,000 sats</span>
          </div>
        </div>
      </div>

      <!-- Escape Options -->
      <div class="border-t border-zinc-900/30">
        <h3 class="text-md font-bold text-zinc-200 mt-2">Escape Options</h3>
        <div class="space-y-2 mt-2">          
          <Button variant="secondary" @click="showCommitmentDetails = true" class="w-full text-white">View Commitment TX</Button>
          <!-- Commitment Details Modal -->
          <div
            v-if="showCommitmentDetails"
            class="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50"
          >
          <div class="bg-zinc-800 rounded-lg w-[95%] max-w-lg max-h-[90vh] overflow-y-auto custom-scrollbar p-4">
              <CommitmentDetails @close="showCommitmentDetails = false" />
            </div>
          </div>
          
          <div class="flex space-x-2">
            <div class="flex gap-4 w-full">
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
                      <Button variant="ghost" @click="() => {}"> Cancel </Button>
                      <Button
                        :class="{ 'opacity-50 pointer-events-none': loading }"
                        variant="destructive"
                        @click="(e) => closeChannel(() => {}, false)"
                      >
                        Confirm
                      </Button>
                    </div>
                  </div>
                </PopoverContent>
              </Popover>
              <Popover class="flex-1">
                <PopoverTrigger asChild>
                  <Button class="w-full" variant="default">
                    <Icon icon="material-symbols:flash-off" class="mr-2 h-4 w-4" />
                    Force Close
                  </Button>
                </PopoverTrigger>
                <PopoverContent>
                  <div class="p-4 space-y-4">
                    <p>Are you sure you want to force close this channel?</p>
                    <div class="flex justify-end gap-2">
                      <Button variant="ghost" @click="() => {}"> Cancel </Button>
                      <Button
                        :class="{ 'opacity-50 pointer-events-none': loading }"
                        variant="secondary"
                        @click="(e) => closeChannel(() => {}, true)"
                      >
                        Confirm
                      </Button>
                    </div>
                  </div>
                </PopoverContent>
              </Popover>
            </div>
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
const isExpanded = ref(false)
const showCommitmentDetails = ref(false)
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

const checkChannel = async (force: boolean) => {
  const chanid = channel.value!.chanid

  const [err, result] = await satsnetStp.getChannelStatus(chanid)
  console.log(result)
  if (err) {
    return false
  }
  if (force && result >= 16) {
    return true
  }
  if (result !== 16) {
    return false
  }
  return true
}

const closeChannel = async (closeHanlder: any, force: boolean = false) => {
  loading.value = true
  console.log('close')

  const chanid = channel.value!.chanid
  const status = await checkChannel(force)
  if (!status) {
    toast({
      title: 'Error',
      description: 'Channel tx has not been confirmed',
      variant: 'destructive',
    })
    loading.value = false
    return
  }
  const [err, result] = await satsnetStp.closeChannel(chanid, 0, force)
  closeHanlder()
  if (err) {
    toast({
      title: 'Error',
      description: err.message,
      variant: 'destructive',
    })
    loading.value = false
    return
  } else {
    toast({
      title: 'Success',
      description: 'Close success',
    })
  }
  loading.value = false
  // await store.setChannels([])
  await channelStore.getAllChannels()
  // btcStore.retry()
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