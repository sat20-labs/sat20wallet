<template>
  <div class="w-full">
    <!-- Mode Selection -->
    <div class="mb-4 flex items-center gap-4">
      <Label class="text-sm text-foreground/50 shrink-0">Trans Mode:</Label>
      <div class="flex gap-2">
        <Button
          :variant="selectedNetwork === 'poolswap' ? 'default' : 'secondary'"
          class="h-8 w-[100px] flex items-center justify-start px-3 rounded-2xl"
          @click="selectedNetwork = 'poolswap'"
        >
          <Icon icon="lucide:repeat" class="w-5 h-5 shrink-0" />
          <span class="text-xs">Poolswap</span>
        </Button>
        <Button
          :variant="selectedNetwork === 'lightning' ? 'default' : 'secondary'"
          class="h-8 w-[100px] flex items-center justify-start px-3 rounded-2xl"
          @click="selectedNetwork = 'lightning'"
        >
          <Icon icon="lucide:zap" class="w-5 h-5 shrink-0" />
          <span class="text-xs">Lightning</span>
        </Button>
      </div>
    </div>

    <!-- Content -->
    <div class="mt-4">
      <!-- Lightning Mode Content -->
      <div v-if="selectedNetwork === 'lightning'">
        <Tabs v-model="selectedChain" class="w-full mb-4 py-2">
          <TabsList class="grid w-full grid-cols-3 h-15">
            <TabsTrigger value="bitcoin" class="h-full">
              <Icon
                icon="lucide:bitcoin"
                class="w-5 h-5 mr-1 justify-self-center"
              />
              <span class="text-sm">Bitcoin</span>
            </TabsTrigger>
            <TabsTrigger value="lightning" class="h-full">
              <Icon
                icon="lucide:zap"
                class="w-5 h-5 mr-1 justify-self-center"
              />
              <span class="text-sm">Lightning</span>
            </TabsTrigger>
            <TabsTrigger value="satoshiNet" class="h-full">
              <Icon
                icon="lucide:globe-lock"
                class="w-5 h-5 mr-1 justify-self-center"
              />
              <span class="text-sm">SatoshiNet</span>
            </TabsTrigger>
          </TabsList>

          <TabsContent
            value="bitcoin"
            class="pt-5"
            :key="`${selectedNetwork}-bitcoin`"
          >
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
                    'text-muted-foreground': selectedAssetType !== type,
                  }"
                >
                  {{ type }}
                  <div
                    class="absolute bottom-0 left-0 right-0 h-0.5 transition-all"
                    :class="{
                      'bg-gradient-to-r from-primary to-primary/50 scale-x-100':
                        selectedAssetType === type,
                      'scale-x-0': selectedAssetType !== type,
                    }"
                  />
                </button>
              </nav>
            </div>
            <!-- Asset List -->
            <L1Card
              :selectedType="selectedAssetType"
              :assets="filteredAssets"
              @splicing_in="handleSplicingIn"
              @send="handleSend"
              @update:selectedType="selectedAssetType = $event"
              @deposit="openDepositDialog"
              @withdraw="openWithdrawDialog"
            />
          </TabsContent>

          <TabsContent
            value="lightning"
            class="mt-4"
            :key="`${selectedNetwork}-lightning`"
          >
            <!-- Lightning Channel Card -->
            <ChannelCard
              :selectedType="selectedAssetType"
              @update:selectedType="selectedAssetType = $event"
            />
          </TabsContent>

          <TabsContent
            value="satoshinet"
            class="mt-4"
            :key="`${selectedNetwork}-satoshinet`"
          >
            <!-- Asset Type Tabs -->
            <div class="border-b border-border/50 mb-4">
              <nav class="flex -mb-px gap-4">
                <button
                  v-for="(type, index) in assetTypes"
                  :key="index"
                  class="pb-3 text-xs font-medium"
                  :class="{
                    'text-foreground border-b-2 border-foreground':
                      selectedAssetType === type,
                    'text-muted-foreground': selectedAssetType !== type,
                  }"
                  @click="selectedAssetType = type"
                >
                  {{ type }}
                </button>
              </nav>
            </div>
            <!-- SatoshiNet Assets -->
            <L2Card
              :selectedType="selectedAssetType"
              :address="address || ''"
              :assets="filteredAssets"
              @update:selectedType="selectedAssetType = $event"
              @deposit="openDepositDialog"
              @withdraw="openWithdrawDialog"
            />
          </TabsContent>
        </Tabs>
      </div>

      <!-- Poolswap Mode Content -->
      <div v-else>
        <Tabs v-model="selectedChain" class="w-full mb-4 py-2">
          <TabsList class="grid w-full grid-cols-2">
            <TabsTrigger value="bitcoin" class="text-[13px] h-full">
              <Icon
                icon="lucide:bitcoin"
                class="w-5 h-5 mr-1 justify-self-center"
              />
              Bitcoin
            </TabsTrigger>
            <TabsTrigger value="satoshinet" class="text-[13px] h-full">
              <Icon
                icon="lucide:globe-lock"
                class="w-5 h-5 mr-1 justify-self-center"
              />
              SatoshiNet
            </TabsTrigger>
          </TabsList>
        </Tabs>

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
                'text-muted-foreground': selectedAssetType !== type,
              }"
            >
              {{ type }}
              <div
                class="absolute bottom-0 left-0 right-0 h-0.5 transition-all"
                :class="{
                  'bg-gradient-to-r from-primary to-primary/50 scale-x-100':
                    selectedAssetType === type,
                  'scale-x-0': selectedAssetType !== type,
                }"
              />
            </button>
          </nav>
        </div>

        <!-- Asset List -->
        <div class="space-y-2">
          <div
            v-for="asset in filteredAssets"
            :key="asset.id"
            class="flex items-center justify-between p-3 rounded-lg bg-muted/25 hover:bg-muted/50 transition-all"
          >
            <div>
              <div class="font-medium">
                {{ (asset.ticker || asset.label).toUpperCase() }}
              </div>
              <div class="text-sm text-muted-foreground">
                {{ formatAmount(asset, selectedAssetType) }}
              </div>
            </div>
            <Button
              v-if="selectedAssetType === 'BTC'"
              size="sm"
              variant="outline"
              @click="handleSend(asset)"
            >
              <Icon icon="lucide:arrow-big-right" class="w-4 h-4" /><span
                class="mr-2 mb-[0.5px]"
                >Send</span
              >
            </Button>
            <Button
              v-if="selectedChain === 'SatoshiNet'"
              size="sm"
              variant="outline"
              @click="openWithdrawDialog(asset)"
            >
              <Icon icon="lucide:arrow-up-right" class="w-4 h-4" /><span
                class="mr-2 mb-[0.5px]"
                >Withdraw</span
              >
            </Button>
            <Button
              v-else
              size="sm"
              variant="outline"
              @click="openDepositDialog(asset)"
            >
              <Icon icon="lucide:arrow-down-right" class="w-4 h-4" /><span
                class="mr-2 mb-[0.5px]"
                >Deposit</span
              >
            </Button>
          </div>
        </div>
      </div>
    </div>

    <!-- Asset Operation Dialog -->
    <AssetOperationDialog
      v-model:open="showOperationDialog"
      v-model:amount="operationAmount"
      :type="operationType"
      :asset="selectedAsset"
      @confirm="handleOperationConfirm"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Icon } from '@iconify/vue'
import L1Card from '@/components/wallet/L1Card.vue'
import L2Card from '@/components/wallet/L2Card.vue'
import ChannelCard from '@/components/wallet/ChannelCard.vue'
import AssetOperationDialog from '@/components/wallet/AssetOperationDialog.vue'
import { useL1Store, useL2Store, useWalletStore } from '@/store'
import { useChannelStore } from '@/store/channel'

const router = useRouter()
const l1Store = useL1Store()
const l2Store = useL2Store()
const walletStore = useWalletStore()
const channelStore = useChannelStore()

const { address } = storeToRefs(walletStore)
const selectedNetwork = ref('poolswap')
const selectedChain = ref('bitcoin')
const selectedAssetType = ref('BTC')
const assetTypes = ['BTC', 'SAT20', 'Runes', 'BRC20']

// 统一的操作弹窗控制
const showOperationDialog = ref(false)
const operationType = ref<'deposit' | 'withdraw'>('deposit')
const operationAmount = ref('')
const selectedAsset = ref<any>(null)

// 资产列表
const filteredAssets = computed(() => {
  let assets: any[] = []

  // 获取当前链的资产列表
  const getChainAssets = (isMainnet: boolean) => {
    const store = isMainnet ? l1Store : l2Store
    switch (selectedAssetType.value) {
      case 'BTC':
        return store.plainList || []
      case 'SAT20':
        return store.sat20List || []
      case 'Runes':
        return store.runesList || []
      case 'BRC20':
        return store.brc20List || []
      default:
        return []
    }
  }

  if (selectedNetwork.value === 'lightning') {
    if (selectedChain.value === 'bitcoin') {
      assets = getChainAssets(true) // L1 资产
    } else if (selectedChain.value === 'satoshinet') {
      assets = getChainAssets(false) // L2 资产
    }
  } else {
    // Poolswap 模式
    const isMainnet = selectedChain.value === 'bitcoin'
    assets = getChainAssets(isMainnet)
  }

  return assets
})

// 打开存款对话框
const openDepositDialog = (asset: any) => {
  selectedAsset.value = asset
  operationType.value = 'deposit'
  operationAmount.value = ''
  showOperationDialog.value = true
}

// 打开提款对话框
const openWithdrawDialog = (asset: any) => {
  selectedAsset.value = asset
  operationType.value = 'withdraw'
  operationAmount.value = ''
  showOperationDialog.value = true
}

// 处理操作确认
const handleOperationConfirm = (type: string, asset: any, amount: string) => {
  if (type === 'deposit') {
    console.log('Deposit:', {
      asset,
      amount,
    })
  } else {
    console.log('Withdraw:', {
      asset,
      amount,
    })
  }
}
const handleSplicingIn = (asset: any) => {
  console.log('Splicing in:', asset)
  router.push(
    `/wallet/asset?type=splicing_in&p=${asset.protocol}&t=${asset.type}&a=${asset.id}`
  )
}
const handleSend = (asset: any) => {
  console.log('Send:', asset)
  router.push(
    `/wallet/asset?type=${selectedChain.value === 'bitcoin' ? 'l1_send' : 'l2_send'}&p=${asset.protocol || 'btc'}&t=${asset.type}&a=${asset.id}`
  )
}


const formatAmount = (asset: any, selectedAssetType: any) => {
  if (selectedAssetType === 'BTC') {
    return `Available: ${asset.amount} sats`
  }
  console.log('selectedAssetType:', selectedAssetType)
  return `${asset.amount} $${asset.ticker || asset.label}`
}

// 添加网络切换监听
watch(selectedNetwork, async (newVal) => {
  selectedChain.value = 'bitcoin'
  selectedAssetType.value = 'BTC'

  try {
    if (newVal === 'lightning') {
      await nextTick()
      await channelStore.getAllChannels()
    }
  } catch (error) {
    console.error('Channel fetch error:', error)
  }
})
</script>
