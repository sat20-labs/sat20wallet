<template>
  <!-- <div class="flex  items-center flex-wrap gap-2 mb-2" v-if="channel">
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
  </div> -->
  <div class="relative" v-if="channel">
    <!-- <div class="relative" v-if="channel"></div> -->
    <div
      class="absolute w-full h-full z-10 flex justify-center items-center bg-gray-900 bg-opacity-95 left-0 top-0"
      v-if="channel.status !== 16"
    >
      <p>{{ channelStatusText }}</p>
    </div>
    <ChannelAssetsTabs @update:model-value="updateSelectedType" v-model="selectedType" :assets="channelAssets" @splicing_out="$emit('splicing_out', $event)" @unlock="$emit('unlock', $event)" />
    <!-- <Progress :value="progressValue" class="w-full" /> -->
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
  <!-- <div v-if="channel && channel.status > 15" class="mt-8">
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
  </div> -->
</template>

<script lang="ts" setup>
import { ref, computed, onMounted } from 'vue'
import { storeToRefs } from 'pinia'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import Progress from '@/components/ui/progress/index.vue'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Icon } from '@iconify/vue'
import { useChannelStore, useWalletStore } from '@/store'
import satsnetStp from '@/utils/stp'
import { useToast } from '@/components/ui/toast'
import { hideAddress } from '~/utils'
import { getChannelStatusText } from '~/composables'
import { useL1Store } from '~/store'
import CopyCard from '@/components/common/CopyCard.vue'
import ChannelAssetsTabs from '@/components/asset/ChannelAssetsTabs.vue'
import { sleep } from 'radash'

// const props = defineProps<{
//   selectedType: string // 改为 selectedType 以匹配 v-model 的默认行为
// }>()

// const { selectedType } = toRefs(props)
const selectedType = defineModel<string>('selectedType')
const emit = defineEmits(['splicing_out', 'unlock', 'update:selectedType'])

const walletStore = useWalletStore()
const { walletId, accountIndex, btcFeeRate } = storeToRefs(walletStore)

const l1Store = useL1Store()
const { plainUtxos, balance: l1PlainBalance } = storeToRefs(l1Store)


const channelStore = useChannelStore()

const { channel, plainList, sat20List, brc20List, runesList } = storeToRefs(channelStore)
const { toast } = useToast()

const loading = ref(false)
const showAmt = ref(false)
const channelAmt = ref('')

// 通道状态进度
const progressValue = computed(() => {
  if (!channel.value) return 0
  const status = channel.value.status
  // 根据状态返回进度值
  switch (status) {
    case 1: return 20  // 初始化
    case 2: return 40  // 等待确认
    case 3: return 60  // 确认中
    case 4: return 80  // 即将完成
    case 16: return 100 // 已完成
    default: return 0
  }
})

const channelAssets = computed(() => {
  console.log('channelAssets');
  console.log(selectedType.value);
  console.log(plainList.value);
  console.log(sat20List.value);
  console.log(runesList.value);
  console.log(brc20List.value);
  
  switch (selectedType.value) {
    case 'BTC':
      return (plainList.value).map(asset => ({ ...asset, type: 'BTC' }))
    case 'ORDX':
      return (sat20List.value).map(asset => ({ ...asset, type: 'ORDX' }))
    case 'Runes':
      console.log('Runes');
      console.log(runesList.value);
      const list = (runesList.value).map(asset => ({ ...asset, type: 'Runes' }))
      console.log('list');
      console.log(list);
      return list
    case 'BRC20':
      return (brc20List.value).map(asset => ({ ...asset, type: 'BRC20' }))
    default:
      return []
  }
})

console.log('channelAssets 11');
console.log(channelAssets);


const openHandler = () => {
  showAmt.value = !showAmt.value
}

const updateSelectedType = (value: string) => {
  console.log('updateSelectedType', value)
  emit('update:selectedType', value)
}

const clear = () => {
  showAmt.value = false
  channelAmt.value = ''
}
const amtConfirm = async () => {
  const amt = parseInt(channelAmt.value, 10)

  if (amt > l1PlainBalance.value) {
    toast({
      title: 'Error',
      description: 'Balance not enough',
      variant: 'destructive',
    })
    return
  }

  const [err11, result11] = await satsnetStp.getChannelStatus(channelAmt.value);
  if (err11) {
    toast({
      title: 'Error',
      description: err11.message,
      variant: 'destructive',
    })
    return
  }
  

  loading.value = true

  const memo = '::open'
  const [err, result] = await satsnetStp.openChannel(btcFeeRate.value, amt, [], memo)

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

watch([walletId, accountIndex], async () => {
  await channelStore.getAllChannels()
})


</script>

<style></style>
