<template>
  <div>
    <Button size="xs" variant="outline" @click="isOpen = true" class="rounded-full">
      <!-- <span><{{ showNetwork?.name }}</span> -->
      <Icon :icon="showNetwork?.icon" :class="showNetwork?.iconColor" />{{ showNetwork?.name }}
      <ChevronDown class="h-4 w-4" />
    </Button>

    <Dialog v-model:open="isOpen">
      <DialogContent class="">
        <DialogHeader>
          <DialogTitle class="text-gray-300 text-xl font-semibold">Select Network</DialogTitle>
        </DialogHeader>
        <div class="space-y-2 py-4">
          <Button
            v-for="n in networks"
            :key="n.value"
            @click="selectNetwork(n)"
            :variant="n.value === network ? 'secondary' : 'outline'"
            class="w-full justify-start"
          >
            <div class="flex items-center gap-3">
              <Icon :icon="n.icon" :class="n.iconColor" />
              <span>{{ n.name }}</span>
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
    name: 'Bitcoin',
    icon: 'lucide:bitcoin',
    iconColor: 'text-orange-500',
    value: Network.LIVENET,
  },
  {
    name: 'Testnet',
    icon: 'lucide:bitcoin',    
    iconColor: 'text-green-500',
    value: Network.TESTNET,
  },
]

const isOpen = ref(false) // 默认不打开对话框

const selectNetwork = async (network: NetworkItem) => {
  console.log(network);
  await walletStore.setNetwork(network.value)
  isOpen.value = false // 选择后关闭对话框
}

const showNetwork = computed(() =>
  networks.find((n) => n.value === network.value)
)
</script>
