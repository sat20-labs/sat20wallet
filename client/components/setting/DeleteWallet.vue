<template>
  <Button variant="destructive" @click="deleteWallet" class="w-full h-12">Delete Wallet</Button>
</template>

<script lang="ts" setup>
import { Button } from '@/components/ui/button'
import { useWalletStore, useL1Store, useL2Store } from '@/store'
import { storage, StorageArea } from 'wxt/storage'
import { useRouter } from 'vue-router'

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
