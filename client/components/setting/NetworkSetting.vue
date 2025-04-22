<template>
  <div class="w-full px-2  bg-zinc-700/40 rounded-lg">
    <button @click="isExpanded = !isExpanded"
      class="flex items-center justify-between w-full p-2 text-left text-primary font-medium rounded-lg">
      <div>
        <h2 class="text-lg font-bold text-zinc-200">Network Options</h2>
        <p class="text-muted-foreground">Switch between mainnet and testnet</p>
      </div>
      <div class="mr-2">
          <Icon v-if="isExpanded" icon="lucide:chevrons-up" class="mr-2 h-4 w-4" />
          <Icon v-else icon="lucide:chevrons-down" class="mr-2 h-4 w-4" />
      </div>
    </button>
    <div v-if="isExpanded" class="space-y-6 px-2 mt-4">
      <div class="flex items-center justify-between">
        <div class="text-sm text-muted-foreground mb-4">
            Network Switch:          
        </div>
        <Select v-model="selectedNetwork">
          <SelectTrigger class="w-[180px] bg-gray-900/30 mb-4">
            <SelectValue placeholder="Select network" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="mainnet">Mainnet</SelectItem>
            <SelectItem value="testnet">Testnet</SelectItem>
          </SelectContent>
        </Select>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
// import { useWallet } from '../../context/wallet-context'

// const { network, setNetwork } = useWallet()
 const network = 'mainnet'
 const selectedNetwork = ref(network)
 const isExpanded = ref(false)

// watch(selectedNetwork, (newValue) => {
//   setNetwork(newValue)
//   console.log(newValue)
// })
</script>