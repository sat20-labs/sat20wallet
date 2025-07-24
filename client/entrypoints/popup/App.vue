<template>
  <main class="w-full h-full overflow-hidden" v-if="!loading">
    <RouterView />
    <Toaster :duration="3000" />
  </main>
</template>

<script lang="ts" setup>
import walletManager from '@/utils/sat20'
import Toaster from '@/components/ui/toast/Toaster.vue'
import { useWalletStore } from '@/store'
const loading = ref(false)
const walletStore = useWalletStore()

const getWalletStatus = async () => {
  const [err, res] = await walletManager.isWalletExist()
  if (err) {
    console.error(err)
    return
  }

  if (res?.exists) {
    await walletStore.setHasWallet(true)
  }
}

onBeforeMount(async () => {
  loading.value = true
  await getWalletStatus()
  loading.value = false
})
</script>
