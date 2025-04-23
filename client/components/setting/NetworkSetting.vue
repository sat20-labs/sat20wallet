<template>
  <div class="w-full px-2  bg-zinc-700/40 rounded-lg">
    <button @click="isExpanded = !isExpanded"
      class="flex items-center justify-between w-full p-2 text-left text-primary font-medium rounded-lg">
      <div>
        <h2 class="text-lg font-bold text-zinc-200">Environment Options</h2>
        <p class="text-muted-foreground">Switch between mainnet and testnet</p>
      </div>
      <div class="mr-2">
        <Icon v-if="isExpanded" icon="lucide:chevrons-up" class="mr-2 h-4 w-4" />
        <Icon v-else icon="lucide:chevrons-down" class="mr-2 h-4 w-4" />
      </div>
    </button>
    <div v-if="isExpanded" class="space-y-6 px-2 mt-4">
      <div class="flex items-center justify-between border-t border-zinc-900/30 pt-4">
        <div class="text-sm text-muted-foreground">
          Environment Switch:
        </div>
        <Select v-model="computedEnv">
          <SelectTrigger class="w-[180px] bg-gray-900/30 mb-4">
            <SelectValue placeholder="Select environment" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="dev">Development</SelectItem>
            <SelectItem value="test">Test</SelectItem>
            <SelectItem value="prod">Production</SelectItem>
          </SelectContent>
        </Select>
      </div>
      <div class="flex items-center justify-between">
        <div class="text-sm text-muted-foreground mb-4">
          Network Switch:
        </div>
        <Select v-model="network" @update:model-value="(value: any) => walletStore.setNetwork(value)">
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
import { useWalletStore } from '@/store/wallet'


import { useGlobalStore, type Env } from '@/store/global'
import { Message } from '@/types/message'
import stp from '@/utils/stp'
import walletManager from '@/utils/sat20'
const isExpanded = ref(false)
const globalStore = useGlobalStore()
const walletStore = useWalletStore()
const { network, } = storeToRefs(walletStore)

const computedEnv = computed<Env>({
  get: () => globalStore.env,
  set: async (newValue) => {
    await globalStore.setEnv(newValue)

    try {
      console.log(`Sending ENV_CHANGED message with payload: ${newValue}`)
      await browser.runtime.sendMessage({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.ENV_CHANGED,
        data: { env: newValue },
        metadata: { from: 'SETTINGS_PAGE' }
      })
    } catch (error) {
      console.error('Failed to send ENV_CHANGED message to background:', error)
    }
    await stp.release()
    await walletManager.release()
    window.location.reload()
  }
})
</script>