<template>
  <div class="flex items-center justify-between space-y-1 py-2">
    <div class="flex flex-col space-y-0.5">
      <label class="text-sm font-medium leading-none">
        {{ currentChain === Chain.BTC ? 'Bitcoin' : 'Satnet' }}
      </label>
      <span class="text-xs text-muted-foreground">
        {{ currentChain === Chain.BTC ? 'Switch to Satnet' : 'Switch to Bitcoin' }}
      </span>
    </div>
    <Switch
      :checked="currentChain === Chain.SATNET"
      @update:checked="handleSwitchChange"
    />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useWalletStore } from '@/store'
import { Chain } from '@/types'
import { storeToRefs } from 'pinia'
import { Switch } from '@/components/ui/switch'

const walletStore = useWalletStore()
const { chain } = storeToRefs(walletStore)
const { setChain } = walletStore

const currentChain = computed(() => chain.value)

const handleSwitchChange = async (checked: boolean) => {
  const newChain = checked ? Chain.SATNET : Chain.BTC
  if (newChain !== currentChain.value) {
    await setChain(newChain)
  }
}
</script> 