<template>
  <Button variant="secondary" @click="deleteWallet" class="w-full h-12"><Icon icon="lucide:trash-2" class="mr-2 h-4 w-4" /> Delete Wallet</Button>
</template>
<!-- <Icon icon="lucide:chevron-down" class="w-6 h-6 shrink-0 text-foreground/60" /> -->
<script lang="ts" setup>
import { Button } from '@/components/ui/button'
import { useWalletStore, useL1Store, useL2Store } from '@/store'
import { storage, StorageArea } from 'wxt/storage'
import { useRouter } from 'vue-router'
import { Icon } from '@iconify/vue'

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
</script>
