<template>
  <div class="w-full">
    <!-- Mode Selection -->
    <div class="mb-4 flex items-center gap-4">
      <Label class="text-sm text-foreground/50 shrink-0">Trans Mode:</Label>
      <div class="flex gap-2">
        <Button
          :variant="selectedNetwork === 'poolswap' ? 'secondary' : 'outline'"
          class="h-8 w-[100px] flex items-center justify-start px-3 rounded-md"
          @click="selectedNetwork = 'poolswap'"
        >
          <Icon icon="lucide:repeat" class="w-5 h-5 shrink-0" />
          <span class="text-xs mb-1">Poolswap</span>
        </Button>
        <Button
          :variant="selectedNetwork === 'lightning' ? 'secondary' : 'outline'"
          class="h-8 w-[100px] flex items-center justify-start px-3 rounded-md"
          @click="selectedNetwork = 'lightning'"
        >
          <Icon icon="lucide:zap" class="w-5 h-5 shrink-0" />
          <span class="text-xs mb-1">Lightning</span>
        </Button>
      </div>
    </div>

    <!-- Content -->
    <div class="mt-4">
      <!-- Lightning Mode Content -->
      <div v-if="selectedNetwork === 'lightning'">
        <Tabs v-model="selectedChain" class="w-full mb-4 py-2">
          <TabsList class="grid w-full grid-cols-3 h-15">
            <TabsTrigger value="bitcoin" class="h-full hover:text-primary/80">
              <Icon icon="lucide:bitcoin" class="w-4 h-4 mr-1 justify-self-center" />
              <span class="text-xs font-normal">BITCOIN</span>
            </TabsTrigger>
            <TabsTrigger value="lightning" class="h-full hover:text-primary/80">
              <Icon icon="lucide:zap" class="w-4 h-4 mr-1 justify-self-center" />
              <span class="text-xs font-normal">LIGHTNING</span>
            </TabsTrigger>
            <TabsTrigger value="satoshiNet" class="h-full hover:text-primary/80">
              <Icon icon="lucide:globe-lock" class="w-4 h-4 mr-1 justify-self-center" />
              <span class="text-xs font-normal">SATOSHINET</span>
            </TabsTrigger>
          </TabsList>

          <TabsContent value="bitcoin" class="pt-5" :key="`${selectedNetwork}-bitcoin`">
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
            <!-- Asset List -->
            <L1Card 
              :selectedType="selectedAssetType" 
              :assets="filteredAssets"
              @update:selectedType="selectedAssetType = $event"
              @deposit="openDepositDialog" 
              @withdraw="openWithdrawDialog" 
            />
          </TabsContent>

          <TabsContent value="lightning" class="mt-4" :key="`${selectedNetwork}-lightning`">
            <!-- Lightning Channel Card -->
            <ChannelCard 
              :selectedType="selectedAssetType"
              @update:selectedType="selectedAssetType = $event"
              @lock="handleLock"
              @unlock="handleUnlock"
            />
          </TabsContent>

          <TabsContent value="satoshinet" class="mt-4" :key="`${selectedNetwork}-satoshinet`">
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
            <TabsTrigger value="bitcoin" class="text-xs h-full hover:text-primary/80">
              <Icon icon="lucide:bitcoin" class="w-4 h-4 mr-1 justify-self-center" />
              BITCOIN
            </TabsTrigger>
            <TabsTrigger value="satoshinet" class="text-xs h-full hover:text-primary/80">
              <Icon icon="lucide:globe-lock" class="w-4 h-4 mr-1 justify-self-center" />
              SATOSHINET
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

        <!-- Asset List -->
        <div class="space-y-2">
          <div
            v-for="asset in filteredAssets"
            :key="asset.id"
            class="flex items-center justify-between p-3 rounded-lg bg-muted/50 hover:bg-muted transition-all"
          >
            <div>
              <div class="font-medium">{{ (asset.ticker || asset.label).toUpperCase() }}</div>
              <div class="text-sm text-muted-foreground">
                {{ formatAmount(asset, selectedAssetType) }}
              </div>
            </div>
            <Button v-if="selectedAssetType === 'BTC'" size="sm" variant="outline" @click="$emit('Send', asset)">
              <Icon icon="lucide:arrow-big-right" class="w-4 h-4" /><span class="mr-2 mb-[0.5px]">Send</span>
            </Button>
            <Button v-if="selectedChain === 'SatoshiNet'" size="sm" variant="outline" @click="openWithdrawDialog(asset)">
              <Icon icon="lucide:arrow-up-right" class="w-4 h-4" /><span class="mr-2 mb-[0.5px]">Withdraw</span>
            </Button>
            <Button v-else size="sm" variant="outline" @click="openDepositDialog(asset)">
              <Icon icon="lucide:arrow-down-right" class="w-4 h-4" /><span class="mr-2 mb-[0.5px]">Deposit</span>
            </Button>            
          </div>
        </div>
      </div>
    </div>

    <!-- Deposit Dialog -->
    <Dialog v-model:open="showDepositDialog">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Deposit {{ selectedAsset?.ticker || selectedAsset?.label }}</DialogTitle>
          <DialogDescription>
            Review pool information before depositing
          </DialogDescription>
        </DialogHeader>
        <div class="space-y-4">
          <div class="space-y-2">
            <Label>Assets Information</Label>
            <div class="rounded-lg border p-3">
              <div class="text-sm">
                <div>Asset: {{ selectedAsset?.ticker || selectedAsset?.label }}</div>
                <div>Balance: {{ selectedAsset?.amount }}</div>
              </div>
            </div>
          </div>
          <div class="space-y-2">
            <Label>Amount</Label>
            <div class="flex items-center gap-2">
              <Input
                v-model="depositAmount"
                type="number"
                placeholder="Enter amount"
              />
              <span class="text-sm text-muted-foreground">
                {{ selectedAsset?.ticker || selectedAsset?.label }}
              </span>
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button @click="handleDeposit" class="w-full">Deposit</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- Withdraw Dialog -->
    <Dialog v-model:open="showWithdrawDialog">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Withdraw {{ selectedAsset?.ticker || selectedAsset?.label }}</DialogTitle>
          <DialogDescription>
            Review pool information before withdrawing
          </DialogDescription>
        </DialogHeader>
        <div class="space-y-4">
          <div class="space-y-2">
            <Label>Assets Information</Label>
            <div class="rounded-lg border p-3">
              <div class="text-sm">
                <div>Asset: {{ selectedAsset?.ticker || selectedAsset?.label }}</div>
                <div>Balance: {{ selectedAsset?.amount }}</div>
              </div>
            </div>
          </div>
          <div class="space-y-2">
            <Label>Amount</Label>
            <div class="flex items-center gap-2">
              <Input
                v-model="withdrawAmount"
                type="number"
                placeholder="Enter amount"
              />
              <span class="text-sm text-muted-foreground">
                {{ selectedAsset?.ticker || selectedAsset?.label }}
              </span>
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button @click="handleWithdraw" class="w-full">Withdraw</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Icon } from '@iconify/vue'
import L1Card from '@/components/wallet/L1Card.vue'
import L2Card from '@/components/wallet/L2Card.vue'
import ChannelCard from '@/components/wallet/ChannelCard.vue'
import { useL1Store, useL2Store, useWalletStore } from '@/store'
import { useChannelStore } from '@/store/channel'

const l1Store = useL1Store()
const l2Store = useL2Store()
const walletStore = useWalletStore()
const channelStore = useChannelStore()

const { address } = storeToRefs(walletStore)
const selectedNetwork = ref('poolswap')
const selectedChain = ref('bitcoin')
const selectedAssetType = ref('BTC')
const assetTypes = ['BTC', 'SAT20', 'Runes', 'BRC20']

// 弹窗控制
const showDepositDialog = ref(false)
const showWithdrawDialog = ref(false)
const selectedAsset = ref<any>(null)
const depositAmount = ref('')
const withdrawAmount = ref('')

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
  showDepositDialog.value = true
}

// 打开提款对话框
const openWithdrawDialog = (asset: any) => {
  selectedAsset.value = asset
  showWithdrawDialog.value = true
}

// 处理存款
const handleDeposit = () => {
  if (!selectedAsset.value) return
  
  console.log('Deposit:', {
    asset: selectedAsset.value,
    amount: depositAmount.value
  })
  showDepositDialog.value = false
}

// 处理提款
const handleWithdraw = () => {
  if (!selectedAsset.value) return
  
  console.log('Withdraw:', {
    asset: selectedAsset.value,
    amount: withdrawAmount.value
  })
  showWithdrawDialog.value = false
}

// 处理锁定
const handleLock = (asset: any) => {
  console.log('Lock asset:', asset)
}

// 处理解锁
const handleUnlock = (asset: any) => {
  console.log('Unlock asset:', asset)
}

const formatAmount = (asset: any,selectedAssetType: any) => {
  if (selectedAssetType === 'BTC') {
    return `Available: ${asset.amount} sats`
  }
  console.log("selectedAssetType:",selectedAssetType)
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