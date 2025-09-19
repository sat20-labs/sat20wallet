<template>
  <div class="space-y-4">
    <!-- Current Pools -->
    <div v-if="currentPool.length > 0" class="space-y-4">
      <h3 class="text-sm font-semibold">Current Pools</h3>
      <div v-for="pool in currentPool" :key="pool.id" class="rounded-lg border hover:border-primary/30 bg-muted p-4 space-y-3">
        <div class="flex items-center justify-between">
          <div class="flex items-center gap-2">
            <div class="w-7 h-7 flex items-center justify-center">
              <Icon v-if="pool.asset.icon" :icon="pool.asset.icon" class="w-6 h-6" />
              <div v-else class="w-6 h-6 rounded-full bg-muted flex items-center justify-center">
                <span class="text-base font-medium">
                  {{ (pool.asset.ticker || pool.asset.name || '').charAt(0).toUpperCase() }}
                </span>
              </div>
            </div>
            <div class="flex flex-col">
              <span class="font-medium">{{ pool.asset.ticker || pool.asset.name }}</span>
              <span class="text-sm text-muted-foreground">{{ pool.asset.type }}</span>
            </div>
          </div>
          <Button variant="outline" size="sm" @click="handleExitPool(pool)">
            <Icon icon="lucide:log-out" class="w-4 h-4 mr-1" />
            Exit Pool
          </Button>
        </div>
        <div class="grid grid-cols-2 gap-4 text-sm">
          <div>
            <div class="text-muted-foreground">Asset</div>
            <div class="flex items-center gap-1">
              <span>{{ pool.asset.ticker || pool.asset.name }}</span>
              <span v-if="pool.amount" class="text-muted-foreground">({{ pool.amount }})</span>
            </div>
          </div>
          <div>
            <div class="text-muted-foreground">Balance</div>
            <div>{{ pool.balance }} {{ pool.unit }}</div>
          </div>
        </div>
      </div>
    </div>

    <!-- Available Pools -->
    <div class="space-y-3">
      <div class="flex items-center justify-between">
        <h3 class="text-sm font-semibold">Available Pools</h3>
        <Button v-if="canCreatePool" variant="outline" size="sm" @click="showCreateDialog = true">
          <Icon icon="lucide:plus" class="w-4 h-4 mr-1" />
          Create Pool
        </Button>
      </div>
      
      <div class="grid gap-4">
        <div v-for="pool in filteredAvailablePools" :key="pool.id" 
          class="rounded-lg border bg-muted p-4 space-y-3 hover:border-primary/40 transition-colors">
          <div class="flex items-center justify-between">
            <div class="flex items-center gap-2">
              <div class="w-7 h-7 flex items-center justify-center">
                <Icon v-if="pool.asset.icon" :icon="pool.asset.icon" class="w-6 h-6" />
                <div v-else class="w-6 h-6 rounded-full bg-muted flex items-center justify-center">
                  <span class="text-base font-medium">
                    {{ (pool.asset.ticker || pool.asset.name || '').charAt(0).toUpperCase() }}
                  </span>
                </div>
              </div>
              <div class="flex flex-col">
                <span class="font-medium">{{ pool.asset.ticker || pool.asset.name }}</span>
                <span class="text-sm text-muted-foreground">{{ pool.asset.type }}</span>
              </div>
            </div>
            <Button variant="outline" size="sm" @click="handleJoinPool(pool)">
              <Icon icon="lucide:log-in" class="w-4 h-4 mr-1" />
              Join Pool
            </Button>
          </div>
          <div class="grid grid-cols-3 gap-4 text-sm">
            <div>
              <div class="text-muted-foreground">Amount</div>
              <div v-if="pool.totalAmount">{{ pool.totalAmount }}</div>
              <div v-else>-</div>
            </div>
            <div>
              <div class="text-muted-foreground">Value</div>
              <div>{{ pool.totalValue }} {{ pool.unit }}</div>
            </div>           
            <!-- <div>
              <div class="text-muted-foreground">Users</div>
              <div>{{ pool.userCount }}</div>
            </div> -->
            <div>
              <div class="text-muted-foreground">Min Deposit</div>
              <div>{{ pool.minDeposit }} {{ pool.unit }}</div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Join Pool Dialog -->
    <Dialog v-model:open="showJoinDialog">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Join Pool</DialogTitle>
          <DialogDescription>
            <hr class="mb-6 mt-1 border-t-1 border-accent">
            Enter the amount you want to deposit into the pool
          </DialogDescription>
        </DialogHeader>
        <div class="space-y-4">
          <div class="space-y-2">
            <Label>Asset Information</Label>
            <div class="rounded-lg border p-3">
              <div class="text-sm">
                <div>Asset Type: {{ selectedPool?.asset.type }}</div>
                <div>Asset: {{ selectedPool?.asset.ticker || selectedPool?.asset.name }}</div>
                <div v-if="selectedPool?.totalAmount">Total Amount: {{ selectedPool.totalAmount }}</div>
                <div>Min Deposit: {{ selectedPool?.minDeposit }} {{ selectedPool?.unit }}</div>
              </div>
            </div>
          </div>
          <div class="space-y-2">
            <Label>Deposit Amount</Label>
            <div class="flex items-center gap-2">
              <Input
                v-model="depositAmount"
                type="number"
                :placeholder="`Min: ${selectedPool?.minDeposit}`"
              />
              <span class="text-sm text-muted-foreground">
                {{ selectedPool?.unit }}
              </span>
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button @click="handleJoinConfirm">Join Pool</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- Create Pool Dialog -->
    <Dialog v-model:open="showCreateDialog">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Create New Pool</DialogTitle>
          <DialogDescription>
            <hr class="mb-6 mt-1 border-t-1 border-accent">
            Set up parameters for your new pool
          </DialogDescription>
        </DialogHeader>
        <div class="space-y-4">
          <div class="space-y-2">
            <Label>Asset Type</Label>
            <Select v-model="newPoolAssetType">
              <SelectTrigger>
                <SelectValue placeholder="Select asset type" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem v-for="type in availableAssetTypes" :key="type" :value="type">
                  {{ type }}
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div v-if="newPoolAssetType && newPoolAssetType !== 'BTC'" class="space-y-2">
            <Label>Asset</Label>
            <Select v-model="newPoolAsset">
              <SelectTrigger>
                <SelectValue placeholder="Select asset" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem v-for="asset in availableAssets" :key="asset.ticker" :value="asset">
                  {{ asset.ticker }} - {{ asset.name }}
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div class="space-y-2">
            <Label>Minimum Deposit</Label>
            <div class="flex items-center gap-2">
              <Input
                v-model="newPoolMinDeposit"
                type="number"
                placeholder="Enter minimum deposit"
              />
              <span class="text-sm text-muted-foreground">sats</span>
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button @click="handleCreateConfirm">Create Pool</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- Exit Pool Dialog -->
    <Dialog v-model:open="showExitDialog">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Exit Pool</DialogTitle>
          <DialogDescription>
            <hr class="mb-6 mt-1 border-t-1 border-accent">
            Are you sure you want to exit this pool? Your funds will be returned to your L1 account.
          </DialogDescription>
        </DialogHeader>
        <div class="space-y-4">
          <div class="space-y-2">
            <Label>Pool Information</Label>
            <div class="rounded-lg border p-3">
              <div class="text-sm">
                <div>Asset Type: {{ selectedExitPool?.asset.type }}</div>
                <div>Asset: {{ selectedExitPool?.asset.ticker || selectedExitPool?.asset.name }}</div>
                <div v-if="selectedExitPool?.amount">Amount: {{ selectedExitPool.amount }}</div>
                <div>Your Balance: {{ selectedExitPool?.balance }} {{ selectedExitPool?.unit }}</div>
              </div>
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="destructive" @click="handleExitConfirm">Exit Pool</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { Icon } from '@iconify/vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { useToast } from '@/components/ui/toast-new'
import { usePoolStore } from '@/store/pool'

const { toast } = useToast()
const poolStore = usePoolStore()

// State
const showJoinDialog = ref(false)
const showCreateDialog = ref(false)
const showExitDialog = ref(false)
const depositAmount = ref('')
const selectedPool = ref<any>(null)
const selectedExitPool = ref<any>(null)
const newPoolAssetType = ref('')
const newPoolAsset = ref<any>(null)
const newPoolMinDeposit = ref('')

// Mock available assets
const mockAssets = {
  ORDX: [
    { ticker: 'ORDI', name: 'Ordinals' },
    { ticker: 'SATS', name: 'Sats Token' },
  ],
  BRC20: [
    { ticker: 'SATS', name: 'Sats Token' },
    { ticker: 'ORDI', name: 'Ordinals' },
  ],
  Runes: [
    { ticker: 'MEME', name: 'Meme Rune' },
    { ticker: 'PEPE', name: 'Pepe Rune' },
  ]
}

// Computed
const currentPool = computed(() => poolStore.currentPool)
const availablePools = computed(() => poolStore.availablePools)
const canCreatePool = computed(() => poolStore.canCreatePool)

// 过滤掉已加入的池子
const filteredAvailablePools = computed(() => {
  const currentAssets = new Set(currentPool.value.map(p => p.asset.ticker || p.asset.type))
  return availablePools.value.filter(p => !currentAssets.has(p.asset.ticker || p.asset.type))
})

const availableAssetTypes = computed(() => {
  const currentTypes = new Set(currentPool.value.map(p => p.asset.type))
  return ['BTC', 'ORDX', 'BRC20', 'Runes'].filter(type => {
    if (type === 'BTC') {
      return !currentTypes.has(type)
    }
    // 对于其他类型，检查是否还有可用的资产
    const assets = mockAssets[type as keyof typeof mockAssets] || []
    const currentAssets = new Set(currentPool.value.map(p => p.asset.ticker))
    return assets.some(asset => !currentAssets.has(asset.ticker))
  })
})

const availableAssets = computed(() => {
  if (!newPoolAssetType.value || newPoolAssetType.value === 'BTC') return []
  const assets = mockAssets[newPoolAssetType.value as keyof typeof mockAssets] || []
  const currentAssets = new Set(currentPool.value.map(p => p.asset.ticker))
  return assets.filter(asset => !currentAssets.has(asset.ticker))
})

// Methods
const getAssetIcon = (type: string) => {
  switch (type) {
    case 'BTC':
      return 'cryptocurrency:btc'
    case 'ORDX':
      return 'lucide:coins'
    case 'BRC20':
      return 'lucide:bitcoin'
    case 'Runes':
      return 'game-icons:rune-sword'
    default:
      return 'lucide:help-circle'
  }
}

const handleJoinPool = (pool: any) => {
  selectedPool.value = pool
  depositAmount.value = pool.minDeposit.toString()
  showJoinDialog.value = true
}

const handleJoinConfirm = async () => {
  const amount = Number(depositAmount.value)
  if (!selectedPool.value || !amount) return

  if (amount < selectedPool.value.minDeposit) {
    toast({
      title: 'Error',
      description: `Minimum deposit is ${selectedPool.value.minDeposit} ${selectedPool.value.unit}`,
      variant: 'destructive'
    })
    return
  }

  const success = await poolStore.joinPool(selectedPool.value.id, amount)
  if (success) {
    toast({
      title: 'Success',
      description: 'Successfully joined the pool',
    })
    showJoinDialog.value = false
    depositAmount.value = ''
    selectedPool.value = null
  } else {
    toast({
      title: 'Error',
      description: 'Failed to join pool',
      variant: 'destructive'
    })
  }
}

const handleCreateConfirm = async () => {
  if (!newPoolAssetType.value || !newPoolMinDeposit.value) return
  if (newPoolAssetType.value !== 'BTC' && !newPoolAsset.value) return

  const success = await poolStore.createPool(
    newPoolAssetType.value,
    Number(newPoolMinDeposit.value)
  )

  if (success) {
    toast({
      title: 'Success',
      description: 'Successfully created new pool',
    })
    showCreateDialog.value = false
    newPoolAssetType.value = ''
    newPoolAsset.value = null
    newPoolMinDeposit.value = ''
  } else {
    toast({
      title: 'Error',
      description: 'Failed to create pool',
      variant: 'destructive'
    })
  }
}

const handleExitPool = (pool: any) => {
  selectedExitPool.value = pool
  showExitDialog.value = true
}

const handleExitConfirm = async () => {
  if (!selectedExitPool.value) return

  const success = await poolStore.exitPool()
  if (success) {
    toast({
      title: 'Success',
      description: 'Successfully exited the pool',
    })
    showExitDialog.value = false
    selectedExitPool.value = null
  } else {
    toast({
      title: 'Error',
      description: 'Failed to exit pool',
      variant: 'destructive'
    })
  }
}

// Lifecycle
onMounted(async () => {
  await Promise.all([
    poolStore.fetchCurrentPool(),
    poolStore.fetchPools()
  ])
})
</script>
