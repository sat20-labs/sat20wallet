<template>
  <div>
    <h2 class="text-2xl font-bold">Network Settings</h2>
    <p class="text-muted-foreground">Configure network and connection preferences</p>
    <div class="space-y-6 mt-4">
      <div class="flex items-center justify-between">
        <div class="space-y-0.5">
          <Label>Network</Label>
          <div class="text-sm text-muted-foreground">
            Switch between mainnet and testnet
          </div>
        </div>
        <Select v-model="selectedNetwork">
          <SelectTrigger class="w-[180px]">
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
// import { useWallet } from '@/context/wallet-context'

// const { network, setNetwork } = useWallet()
const network = 'mainnet'
const selectedNetwork = ref(network)

watch(selectedNetwork, (newValue) => {
  // setNetwork(newValue)
  console.log(newValue)
})
</script>