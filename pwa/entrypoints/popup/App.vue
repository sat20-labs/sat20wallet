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
import { useToast } from "@/components/ui/toast-new";

type BeforeInstallPromptEvent = Event & {
  prompt: () => Promise<void>;
  userChoice: Promise<{ outcome: "accepted" | "dismissed"; platform: string }>;
};

const loading = ref(false);
const walletStore = useWalletStore();
const globalStore = useGlobalStore();
const router = useRouter();
const { checkForUpdates } = useAppVersion();
const { toast } = useToast();
const { autoLockTime } = storeToRefs(globalStore);
let autoLockTimer: ReturnType<typeof setTimeout> | undefined;
let installPromptEvent: BeforeInstallPromptEvent | undefined;

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

const isInstallRequest = () => {
  return new URLSearchParams(window.location.search).get("install") === "1";
};

const isStandaloneApp = () => {
  return window.matchMedia("(display-mode: standalone)").matches || (navigator as any).standalone === true;
};

const isIOS = () => {
  return /iphone|ipad|ipod/i.test(navigator.userAgent);
};

const showInstallFallback = () => {
  if (isStandaloneApp()) return;

  toast({
    variant: "info",
    title: "Install SAT20 Wallet",
    description: isIOS()
      ? "Tap Share, then Add to Home Screen."
      : "Use the browser menu to install SAT20 Wallet or add it to your home screen.",
    duration: 12000,
    action: installPromptEvent
      ? {
          label: "Install",
          onClick: () => {
            void promptInstall();
          },
        }
      : undefined,
  });
};

const promptInstall = async () => {
  if (isStandaloneApp()) return;

  if (!installPromptEvent) {
    showInstallFallback();
    return;
  }

  const promptEvent = installPromptEvent;
  installPromptEvent = undefined;
  await promptEvent.prompt();
  await promptEvent.userChoice.catch(() => undefined);
};

const onBeforeInstallPrompt = (event: Event) => {
  event.preventDefault();
  installPromptEvent = event as BeforeInstallPromptEvent;

  if (isInstallRequest()) {
    void promptInstall().catch(() => {
      showInstallFallback();
    });
  }
};

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
  window.addEventListener("beforeinstallprompt", onBeforeInstallPrompt);

  activityEvents.forEach((eventName) => {
    window.addEventListener(eventName, resetAutoLockTimer, { passive: true });
  });
  resetAutoLockTimer();

  if (isInstallRequest()) {
    window.setTimeout(() => {
      if (installPromptEvent) {
        void promptInstall().catch(() => {
          showInstallFallback();
        });
      } else {
        showInstallFallback();
      }
    }, 1200);
  }

  // 静默检查版本更新（有新版本才提醒）
  setTimeout(() => checkForUpdates(true), 2000);
});

watch(
  () => [autoLockTime.value, walletStore.locked, walletStore.hasWallet, router.currentRoute.value.path],
  resetAutoLockTimer
);

onBeforeUnmount(() => {
  clearAutoLockTimer();
  window.removeEventListener("beforeinstallprompt", onBeforeInstallPrompt);
  activityEvents.forEach((eventName) => {
    window.removeEventListener(eventName, resetAutoLockTimer);
  });
});
</script>
