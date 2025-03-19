<template>
  <div class="flex  items-center flex-wrap gap-2 mb-2" v-if="channel">
    <CopyCard :text="channel.address" class="flex-1">
      <Button asChild size="sm" variant="link">
        <a
          :href="`https://mempool.space/zh/testnet4/address/${channel.address}`"
          target="_blank"
        >
          {{ hideAddress(channel.address) }}
        </a>
      </Button>
    </CopyCard>
    <Button asChild size="sm" variant="link">
      <a
        :href="`https://satstestnet-mempool.sat20.org/address/${channel.address}`"
        target="_blank"
      >
        Open L2
      </a>
    </Button>
  </div>
  <div class="relative" v-if="channel">
    <div
      class="absolute w-full h-full z-10 flex justify-center items-center bg-gray-100 dark:bg-gray-900 bg-opacity-95 left-0 top-0"
      v-if="channel.status !== 16"
    >
      <p>{{ channelStatusText }}</p>
    </div>
    <ChannelAssetsTabs />
  </div>
  <div>
    <Button @click="openHandler" v-if="!channel" class="w-full"> Open </Button>
    <div v-if="showAmt">
      <div class="flex w-full mt-2">
        <div class="flex-1 relative">
          <Input v-model="channelAmt" class="pr-12" />
          <span
            class="absolute right-3 top-1/2 transform -translate-y-1/2 text-gray-500 dark:text-gray-400 text-xs"
            >sats</span
          >
        </div>
        <Button
          :disabled="!channelAmt"
          @click="amtConfirm"
          :class="{ 'opacity-50 pointer-events-none': loading }"
          class="ml-2"
        >
          Confirm
        </Button>
      </div>
    </div>
  </div>
  <div v-if="channel && channel.status > 15">
    <div class="flex gap-4 w-full">
      <Popover v-if="channel" class="flex-1">
        <PopoverTrigger asChild>
          <Button class="w-full" :disabled="channel.status !== 16">
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
          <Button class="w-full" variant="destructive">
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
                variant="destructive"
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
</template>

<script lang="ts" setup>
import { ref, computed, onMounted } from 'vue'
import { storeToRefs } from 'pinia'
import { sleep } from 'radash'
import { Button } from '@/components/ui/button'
import ChannelAssetsTabs from '@/components/asset/ChannelAssetsTabs.vue'
import { Input } from '@/components/ui/input'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { Icon } from '@iconify/vue'
import { hideAddress } from '~/utils'
import { getChannelStatusText } from '~/composables'
import { useL1Store } from '~/store'
import { useChannelStore } from '~/store'
import { useToast } from '@/components/ui/toast'
import satsnetStp from '@/utils/stp'
import CopyCard from '@/components/common/CopyCard.vue'

const l1Store = useL1Store()
const channelStore = useChannelStore()
const { toast } = useToast()

const channelAmt = ref()
const showAmt = ref(false)
const amtType = ref<'open' | 'unlock'>('open')
const { plainUtxos, balance: l1PlainBalance } = storeToRefs(l1Store)
console.log('plainUtxos', plainUtxos)

const loading = ref(false)

const { channel } = storeToRefs(channelStore)

const channelBalance = computed(() => {
  return channel.value?.localbalance_L1?.reduce(
    (acc: number, cur: { Amount: number }) => acc + cur.Amount,
    0
  )
})

const openHandler = () => {
  amtType.value = 'open'
  showAmt.value = !showAmt.value
}

const clear = () => {
  showAmt.value = false
  channelAmt.value = ''
}
const amtConfirm = async () => {
  if (amtType.value === 'open') {
    openChannel()
  } else {
  }
}

const channelStatusText = computed(() => {
  if (!channel.value) return ''
  const status = channel.value?.status
  if (status > 0 && status < 5) {
    return 'Channel is opening'
  } else if (status > 7 && status < 15) {
    return 'Channel is closing'
  } else if (status === 33) {
    return 'Splicing in'
  } else if (status === 51) {
    return 'Splicing out'
  } else {
    return getChannelStatusText(status)
  }
})

const openChannel = async (): Promise<void> => {
  const feeRate = 1
  const amt = parseInt(channelAmt.value, 10)

  if (amt > l1PlainBalance.value) {
    toast({
      title: 'Error',
      description: 'Balance not enough',
      variant: 'destructive',
    })
    return
  }
  loading.value = true
  const utxoList = plainUtxos.value.map((utxo: any) => utxo)

  const memo = '::open'
  const [err, result] = await satsnetStp.openChannel(feeRate, amt, [], memo)

  if (err) {
    toast({
      title: 'Error',
      description: err.message,
      variant: 'destructive',
    })
    loading.value = false
    return
  }
  await sleep(1000)
  channelStore.getAllChannels()
  clear()
  loading.value = false
}
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

<style></style>
