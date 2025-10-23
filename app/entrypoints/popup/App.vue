<template>
  <div>
    <main class="w-full h-screen overflow-hidden" v-if="!loading">
      <RouterView />
      <!-- 全局 Approve 弹窗 -->
      <Approve />
    </main>
    <!-- Toaster 始终渲染，不受 loading 状态影响 -->
    <Toaster
      :duration="6000"
      position="top-right"
      theme="dark"
      :rich-colors="true"
      :close-button="true"
      class="custom-toaster"
    />
  </div>
</template>

<script lang="ts" setup>
import { ref, onBeforeMount } from "vue";
import { useRouter } from "vue-router";
import walletManager from "@/utils/sat20";
import Toaster from "@/components/ui/toast-new/Toaster.vue";
import Approve from "@/entrypoints/popup/pages/wallet/Approve.vue";
import { useWalletStore } from "@/store";

const loading = ref(false);
const walletStore = useWalletStore();
const router = useRouter();

const getWalletStatus = async () => {
  const [err, res] = await walletManager.isWalletExist();
  if (err) {
    console.error(err);
    // Even if there's an error, navigate to create as a fallback
    router.push("/");
    return;
  }

  if (res?.exists) {
    await walletStore.setHasWallet(true);
    router.push("/unlock");
  } else {
    await walletStore.setHasWallet(false);
    router.push("/");
  }
};

onBeforeMount(async () => {
  loading.value = true;
  await getWalletStatus();
  loading.value = false;
});
</script>
