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
import { ref, onBeforeMount, onBeforeUnmount, onMounted, watch } from "vue";
import { useRouter } from "vue-router";
import walletManager from "@/utils/sat20";
import Toaster from "@/components/ui/toast-new/Toaster.vue";
import Approve from "@/entrypoints/popup/pages/wallet/Approve.vue";
import { useGlobalStore, useWalletStore } from "@/store";
import { useAppVersion } from "@/composables/useAppVersion";
import { storeToRefs } from "pinia";

const loading = ref(false);
const walletStore = useWalletStore();
const globalStore = useGlobalStore();
const router = useRouter();
const { checkForUpdates } = useAppVersion();
const { autoLockTime } = storeToRefs(globalStore);
let autoLockTimer: ReturnType<typeof setTimeout> | undefined;

const shouldAutoLock = () => {
  const path = router.currentRoute.value.path;
  return walletStore.hasWallet &&
    !walletStore.locked &&
    !["/", "/unlock", "/import", "/create"].includes(path);
};

const clearAutoLockTimer = () => {
  if (autoLockTimer) {
    clearTimeout(autoLockTimer);
    autoLockTimer = undefined;
  }
};

const lockForInactivity = async () => {
  clearAutoLockTimer();
  if (!shouldAutoLock()) return;

  await walletStore.lockWallet();
  router.replace({
    path: "/unlock",
    query: { redirect: router.currentRoute.value.fullPath },
  });
};

const resetAutoLockTimer = () => {
  clearAutoLockTimer();
  if (!shouldAutoLock()) return;

  const minutes = Number(autoLockTime.value || "5");
  if (!Number.isFinite(minutes) || minutes <= 0) return;

  autoLockTimer = setTimeout(lockForInactivity, minutes * 60 * 1000);
};

const activityEvents = ["pointerdown", "keydown", "touchstart", "scroll"];

const getWalletStatus = async () => {
  const [err, res] = await walletManager.isWalletExist();
  if (err) {
    console.error(err);
    router.push("/");
    return;
  }

  if (res?.exists) {
    await walletStore.setHasWallet(true);
    const currentPath = router.currentRoute.value.path;
    if (currentPath === "/" || currentPath === "/import" || currentPath === "/create") {
      router.replace("/unlock");
    }
  } else {
    await walletStore.setHasWallet(false);
    const currentPath = router.currentRoute.value.path;
    if (currentPath !== "/" && currentPath !== "/import" && currentPath !== "/create") {
      router.replace("/");
    }
  }
};

onBeforeMount(async () => {
  loading.value = true;
  await getWalletStatus();
  loading.value = false;
});

onMounted(() => {
  activityEvents.forEach((eventName) => {
    window.addEventListener(eventName, resetAutoLockTimer, { passive: true });
  });
  resetAutoLockTimer();

  // 静默检查版本更新（有新版本才提醒）
  setTimeout(() => checkForUpdates(true), 2000);
});

watch(
  () => [autoLockTime.value, walletStore.locked, walletStore.hasWallet, router.currentRoute.value.path],
  resetAutoLockTimer
);

onBeforeUnmount(() => {
  clearAutoLockTimer();
  activityEvents.forEach((eventName) => {
    window.removeEventListener(eventName, resetAutoLockTimer);
  });
});
</script>
