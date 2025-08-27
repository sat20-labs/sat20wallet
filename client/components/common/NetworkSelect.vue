<template>
  <div>
    <Button size="xs" variant="outline" @click="isOpen = true" class="px-2 rounded-md">
      <!-- <Icon :icon="showNetwork?.icon" :class="showNetwork?.iconColor" />{{ showNetwork?.name }} -->
      <Icon :icon="showNetwork?.icon" :class="showNetwork?.iconColor" class="w-5 h-5" />
      <ChevronDown class="h-4 w-4" />
    </Button>

    <Dialog v-model:open="isOpen">
      <DialogContent class="w-[330px]">
        <DialogHeader>
          <DialogTitle class="text-gray-300 text-xl font-semibold">{{ $t('networkSelect.selectNetwork') }}</DialogTitle>
        </DialogHeader>
        <div class="space-y-2 py-4">
          <Button
            v-for="n in networks"
            :key="n.value"
            @click="selectNetwork(n)"
            :variant="n.value === network ? 'secondary' : 'outline'"
            class="w-full justify-start h-12"
          >
            <div class="flex items-center gap-3">
              <Icon :icon="n.icon" :class="n.iconColor" />
              <span>{{ $t(`networkSelect.${n.name}`) }}</span>
            </div>
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  </div>
</template>

<script setup lang="ts">
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { ChevronDown } from 'lucide-vue-next'
import walletManager from '@/utils/sat20'
import { Network } from '@/types'
import { useWalletStore } from '@/store'

interface NetworkItem {
  name: string
  icon: string
  iconColor: string
  value: Network
}
const walletStore = useWalletStore()
const { network } = storeToRefs(walletStore)
const networks: NetworkItem[] = [
  {
    name: 'Mainnet',
    icon: 'cryptocurrency:btc',
    iconColor: 'text-orange-500',
    value: Network.LIVENET,
  },
  {
    name: 'Testnet',
    icon: 'cryptocurrency:btc',    
    iconColor: 'text-green-500',
    value: Network.TESTNET,
  },
]

const isOpen = ref(false) // 默认不打开对话框

const selectNetwork = async (network: NetworkItem) => {
  console.log(network);
  setTimeout(() => {
    location.reload()
  }, 600);
  await walletStore.setNetwork(network.value)
  isOpen.value = false // 选择后关闭对话框
  
}

const showNetwork = computed(() =>
  networks.find((n) => n.value === network.value)
)
</script>
