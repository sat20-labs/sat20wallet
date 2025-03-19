<template>
  <Button variant="destructive" @click="deleteWallet" class="w-full">Delete Wallet</Button>
</template>

<script lang="ts" setup>
import { Button } from '@/components/ui/button'
import { useWalletStore, useL1Store, useL2Store } from '@/store'
import { useRouter } from 'vue-router'

const walletStore = useWalletStore()
const l1Store = useL1Store()
const l2Store = useL2Store()
const router = useRouter()

const deleteWallet = async () => {
  await walletStore.deleteWallet()
  l1Store.reset()
  l2Store.reset()
  router.push('/')
}
</script>
