<template>
  <main class="w-full h-full overflow-hidden" v-if="!loading">
    <RouterView />
  </main>
</template>

<script lang="ts" setup>
import { loadWasm } from '@/utils/wasm'
import walletManager from '@/utils/sat20'
import { useRouter } from 'vue-router'
import { useGlobalStore, useWalletStore } from '@/store'
import { walletStorage } from '@/lib/walletStorage'
const router = useRouter()
const loading = ref(false)
const globalStore = useGlobalStore()
const walletStore = useWalletStore()


const getWalletStatus = async () => {
  const [err, res] = await walletManager.isWalletExist()
  if (err) {
    console.error(err)
    return
  }
  console.log(res);
  
  if (res?.exists) {
    walletStore.setHasWallet(true)
    // router.push('/unlock')
  }
}

onMounted(async () => {
  // walletStorage.locked = true
  loading.value = true
  await loadWasm()
  await getWalletStatus()
  loading.value = false
  
})
</script>
