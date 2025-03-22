<template>
  <div class="space-y-4">
    <!-- Channel Info -->
    <div class="flex items-center gap-2" v-if="channel">
      <CopyCard :text="channel.address" class="flex-1">
        <div class="flex items-center gap-2">
          <span class="text-sm text-muted-foreground">{{ hideAddress(channel.address) }}</span>
          <Button asChild size="sm" variant="ghost" class="h-6 px-2">
            <a
              :href="`https://mempool.space/zh/testnet4/address/${channel.address}`"
              target="_blank"
            >
              <Icon icon="lucide:external-link" class="w-4 h-4" />
            </a>
          </Button>
        </div>
      </CopyCard>
      <Button asChild size="sm" variant="outline" class="h-8">
        <a
          :href="`https://satstestnet-mempool.sat20.org/address/${channel.address}`"
          target="_blank"
        >
          <Icon icon="lucide:layers" class="w-4 h-4 mr-1" />
          Open L2
        </a>
      </Button>
    </div>

    <!-- Asset Type Tabs -->
    <div class="border-b border-border/50 mb-4">
      <nav class="flex -mb-px gap-4">
        <button
          v-for="(type, index) in assetTypes"
          :key="index"
          @click="selectedAssetType = type"
          class="pb-2 px-1 font-mono font-semibold text-sm relative"
          :class="{
            'text-foreground/90': selectedAssetType === type,
            'text-muted-foreground': selectedAssetType !== type
          }"
        >
          {{ type }}
          <div
            class="absolute bottom-0 left-0 right-0 h-0.5 transition-all"
            :class="{
              'bg-gradient-to-r from-primary to-primary/50 scale-x-100': selectedAssetType === type,
              'scale-x-0': selectedAssetType !== type
            }"
          />
        </button>
      </nav>
    </div>

    <!-- Channel Status -->
    <div class="relative" v-if="channel">
      <div
        class="absolute w-full h-full z-10 flex justify-center items-center bg-background/95 left-0 top-0"
        v-if="channel.status !== 16"
      >
        <p class="text-sm text-muted-foreground">{{ channelStatusText }}</p>
      </div>

      <!-- Channel Assets -->
      <div class="space-y-2">
        <!-- Asset List -->
        <div
          v-for="asset in filteredAssets"
          :key="asset.id"
          class="flex items-center justify-between p-3 rounded-lg bg-muted/40 hover:bg-muted/60 transition-colors"
        >
          <div>
            <div class="font-medium">{{ getAssetLabel(asset) }}</div>
            <div class="text-sm text-muted-foreground">
              {{ formatAmount(asset) }}
            </div>
          </div>
          <div class="flex gap-0.5">
            <Button size="sm" variant="outline" @click="handleAssetAction(asset, 'unlock')">
              <Icon icon="lucide:unlock" class="w-4 h-4" />
              <span class="ml-1">Unlock</span>
            </Button>
            <Button size="sm" variant="outline" @click="handleAssetAction(asset, 'splicing_out')">
              <Icon icon="lucide:corner-up-right" class="w-4 h-4" />
              <span class="ml-1">Splicing Out</span>
            </Button>
          </div>
        </div>
      </div>

      <!-- Channel Progress -->
      <Progress :value="progressValue" class="w-full mt-4" />
    </div>

    <!-- Channel Actions -->
    <div class="space-y-2">
      <!-- Open Channel -->
      <div v-if="!channel">
        <Button @click="openHandler" class="w-full">
          <Icon icon="lucide:plus" class="w-4 h-4 mr-1" />
          Open Channel
        </Button>
      </div>

      <!-- Amount Input -->
      <div v-if="showAmt" class="space-y-2">
        <div class="flex gap-2">
          <div class="flex-1 relative">
            <Input v-model="channelAmt" class="pr-12" placeholder="Enter amount" />
            <span class="absolute right-3 top-1/2 transform -translate-y-1/2 text-muted-foreground text-xs">
              sats
            </span>
          </div>
          <Button
            :disabled="!channelAmt"
            @click="amtConfirm"
            :class="{ 'opacity-50 pointer-events-none': loading }"
          >
            Confirm
          </Button>
        </div>
      </div>

      <!-- Channel Control -->
      <div v-if="channel && channel.status > 15" class="flex gap-2">
        <Popover class="flex-1">
          <PopoverTrigger asChild>
            <Button class="w-full" :disabled="channel.status !== 16">
              <Icon icon="lucide:power" class="w-4 h-4 mr-1" />
              Close
            </Button>
          </PopoverTrigger>
          <PopoverContent>
            <div class="p-4 space-y-4">
              <p class="text-sm">Are you sure you want to close this channel?</p>
              <div class="flex justify-end gap-2">
                <Button size="sm" variant="ghost" @click="() => {}">Cancel</Button>
                <Button
                  size="sm"
                  :class="{ 'opacity-50 pointer-events-none': loading }"
                  variant="destructive"
                  @click="closeChannel(() => {}, false)"
                >
                  Close
                </Button>
              </div>
            </div>
          </PopoverContent>
        </Popover>

        <Popover class="flex-1">
          <PopoverTrigger asChild>
            <Button class="w-full" variant="destructive">
              <Icon icon="lucide:zap-off" class="w-4 h-4 mr-1" />
              Force Close
            </Button>
          </PopoverTrigger>
          <PopoverContent>
            <div class="p-4 space-y-4">
              <p class="text-sm">Are you sure you want to force close this channel?</p>
              <div class="flex justify-end gap-2">
                <Button size="sm" variant="ghost" @click="() => {}">Cancel</Button>
                <Button
                  size="sm"
                  :class="{ 'opacity-50 pointer-events-none': loading }"
                  variant="destructive"
                  @click="closeChannel(() => {}, true)"
                >
                  Force Close
                </Button>
              </div>
            </div>
          </PopoverContent>
        </Popover>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import Progress from '@/components/ui/progress/index.vue'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { useChannelStore } from '@/store/channel'
import CopyCard from '@/components/common/CopyCard.vue'
import { Icon } from '@iconify/vue'
import { useToast } from '@/components/ui/toast'
import satsnetStp from '@/utils/stp'

const router = useRouter()
const props = defineProps<{
  selectedType: string
}>()

const emit = defineEmits(['unlock', 'splicing-out', 'update:selectedType'])

const channelStore = useChannelStore()
const { channel, plainList, sat20List, brc20List, runesList } = storeToRefs(channelStore)
const { toast } = useToast()

const loading = ref(false)
const showAmt = ref(false)
const channelAmt = ref('')

// 隐藏地址中间部分
const hideAddress = (address: string) => {
  if (!address) return ''
  return `${address.slice(0, 6)}...${address.slice(-4)}`
}

// 计算进度值
const progressValue = computed(() => {
  if (!channel.value) return 0
  const status = channel.value.status
  switch (status) {
    case 0:
      return 10
    case 1:
      return 30
    case 2:
      return 50
    case 3:
      return 70
    case 4:
      return 90
    case 16:
      return 100
    default:
      return 0
  }
})

// 计算通道状态文本
const channelStatusText = computed(() => {
  if (!channel.value) return ''
  const status = channel.value?.status
  if (status > 0 && status < 5) {
    return 'Opening...'
  }
  if (status > 15) {
    return 'Ready'
  }
  return 'Pending'
})

// 资产类型
const assetTypes = ['BTC', 'SAT20', 'BRC20', 'Runes']
const selectedAssetType = ref(props.selectedType)

// 监听资产类型变化
watch(selectedAssetType, (newType) => {
  console.log('Selected asset type changed:', newType)
  emit('update:selectedType', newType)
})

// 监听通道数据变化
watch(channel, (newChannel) => {
  console.log('Channel changed:', newChannel)
}, { immediate: true })

// 监听资产列表变化
watch([plainList, sat20List, brc20List, runesList], ([plain, sat20, brc20, runes]) => {
  console.log('Asset lists updated:', {
    plain,
    sat20,
    brc20,
    runes
  })
}, { immediate: true })

// 根据选择的资产类型过滤资产列表
const filteredAssets = computed(() => {
  console.log('Computing filtered assets')
  console.log('Current asset type:', selectedAssetType.value)
  console.log('Available assets:', {
    plain: plainList.value,
    sat20: sat20List.value,
    brc20: brc20List.value,
    runes: runesList.value
  })
  
  let result = []
  switch (selectedAssetType.value) {
    case 'BTC':
      // 确保 plainList 存在且包含 BTC 资产
      result = plainList.value?.filter(item => !item.protocol && item.key === '::') || []
      break
    case 'SAT20':
      result = sat20List.value || []
      break
    case 'BRC20':
      result = brc20List.value || []
      break
    case 'Runes':
      result = runesList.value || []
      break
  }
  
  console.log('Filtered assets:', result)
  return result
})

// 获取资产显示标签
const getAssetLabel = (asset: any) => {
  if (selectedAssetType.value === 'BTC') {
    return 'BTC'
  }
  return asset.label.toUpperCase()
}

// 格式化金额显示
const formatAmount = (asset: any) => {
  if (selectedAssetType.value === 'BTC') {
    return `${asset.amount} sats`
  }
  return `${asset.amount} $${asset.label}`
}

// 获取资产的协议类型和参数
const getAssetParams = (asset: any) => {
  console.log('Getting asset params for:', asset)
  
  if (!asset) {
    console.warn('Asset is undefined')
    return { protocol: '', type: '', id: '' }
  }
  
  // 特殊处理 BTC 资产
  if (selectedAssetType.value === 'BTC' && (!asset.protocol || asset.key === '::')) {
    return { protocol: 'btc', type: 'utxo', id: '::' }
  }
  
  if (!asset.protocol || !asset.type || !asset.key) {
    console.warn('Asset missing required properties:', {
      protocol: asset.protocol,
      type: asset.type,
      key: asset.key
    })
    return { protocol: '', type: '', id: '' }
  }
  
  return {
    protocol: asset.protocol,
    type: asset.type,
    id: asset.key
  }
}

// 生成资产操作的 URL
const getAssetActionUrl = (asset: any, type: 'unlock' | 'splicing_out') => {
  console.log('Asset for URL:', asset)
  
  const { protocol, type: assetType, id } = getAssetParams(asset)
  if (!protocol || !assetType) {
    console.warn('Missing required asset parameters')
    return '#'
  }
  
  const url = `/wallet/asset?type=${type}&p=${protocol}&t=${assetType}&a=${id}`
  console.log('Generated URL:', url)
  return url
}

// 处理资产操作
const handleAssetAction = async (asset: any, type: 'unlock' | 'splicing_out') => {
  console.log('Handling asset action:', { asset, type })
  loading.value = true
  
  try {
    const url = getAssetActionUrl(asset, type)
    console.log('Navigation URL:', url)
    
    if (url === '#') {
      throw new Error('Invalid asset parameters')
    }
    
    await router.push(url)
  } catch (error) {
    console.error('Navigation error:', error)
    toast({
      title: 'Error',
      description: error instanceof Error ? error.message : 'Failed to navigate to asset operation page',
      variant: 'destructive',
    })
  } finally {
    loading.value = false
  }
}

// 组件挂载和更新时的处理
onMounted(async () => {
  console.log('Component mounted')
  await channelStore.getAllChannels()
})

// 打开通道
const openHandler = () => {
  showAmt.value = true
}

// 清除输入
const clear = () => {
  showAmt.value = false
  channelAmt.value = ''
}

// 确认金额
const amtConfirm = async () => {
  if (!channelAmt.value) {
    return
  }

  loading.value = true
  try {
    const [err, result] = await satsnetStp.openChannel(1, Number(channelAmt.value), [], '::open')
    if (err) {
      toast({
        title: 'Error',
        description: err.message,
        variant: 'destructive',
      })
      return
    }
    clear()
    await channelStore.getAllChannels()
  } catch (error) {
    console.error('Failed to open channel:', error)
    toast({
      title: 'Error',
      description: 'Failed to open channel',
      variant: 'destructive',
    })
  } finally {
    loading.value = false
  }
}

// 关闭通道
const closeChannel = async (closeHandler: any, force: boolean = false) => {
  if (!channel.value) return

  loading.value = true
  try {
    const [err, result] = await satsnetStp.closeChannel(channel.value.chanid, 0, force)
    if (err) {
      toast({
        title: 'Error',
        description: err.message,
        variant: 'destructive',
      })
      return
    }
    if (closeHandler) closeHandler()
    toast({
      title: 'Success',
      description: 'Channel closed successfully',
    })
    await channelStore.getAllChannels()
  } catch (error) {
    console.error('Failed to close channel:', error)
    toast({
      title: 'Error',
      description: 'Failed to close channel',
      variant: 'destructive',
    })
  } finally {
    loading.value = false
  }
}
</script>
