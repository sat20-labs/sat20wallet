<template>
    <div class="w-full px-2  bg-zinc-700/40 rounded-lg">
        <button @click="isExpanded = !isExpanded"
            class="flex items-center justify-between w-full p-2 text-left text-primary font-medium rounded-lg">
            <div>
                <h2 class="text-lg font-bold text-zinc-200">Wallet Options</h2>
                <p class="text-muted-foreground">Phrase and Delete wallet</p>
            </div>
            <div class="mr-2">
                <Icon v-if="isExpanded" icon="lucide:chevrons-up" class="mr-2 h-4 w-4" />
                <Icon v-else icon="lucide:chevrons-down" class="mr-2 h-4 w-4" />
            </div>
        </button>
        <div v-if="isExpanded" class="space-y-6 px-2 py-2">
            <div class="space-y-4 mb-4">
                <Button as-child class="h-12 w-full">
                    <RouterLink to="/wallet/setting/phrase" class="w-full">
                        <Icon icon="lucide:eye-off" class="mr-2 h-4 w-4" /> Show Phrase
                    </RouterLink>
                </Button>

                <Button variant="secondary" @click="deleteWallet" class="w-full h-12">
                    <Icon icon="lucide:trash-2" class="mr-2 h-4 w-4" /> Delete Wallet
                </Button>               
            </div>
        </div> 
    </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { Button } from '@/components/ui/button'
import { Icon } from '@iconify/vue'
import { useWalletStore, useL1Store, useL2Store } from '@/store'
import { storage, StorageArea } from 'wxt/storage'
import { useRouter } from 'vue-router'


import { useGlobalStore, type Env } from '@/store/global'

const isExpanded = ref(false)
const globalStore = useGlobalStore()

const walletStore = useWalletStore()
const l1Store = useL1Store()
const l2Store = useL2Store()
const router = useRouter()

const deleteWallet = async () => {
  await walletStore.deleteWallet()
  l1Store.reset()
  l2Store.reset()
  await storage.clear('local')
  await storage.clear('session')
  window.location.reload()
}

const computedEnv = computed<Env>({
    get: () => globalStore.env,
    set: (newValue) => {
        globalStore.setEnv(newValue)
        window.location.reload()
    }
})
</script>