<template>
  <div>
    <main class="w-full h-screen overflow-hidden" v-if="!loading">
      <RouterView />
      <!-- 全局 Approve 弹窗 -->
      <Approve />
    </main>
    <div
      v-if="showInstallPanel && !isStandaloneApp()"
      class="fixed inset-0 z-[1000] flex items-end bg-black/60 p-3 sm:items-center sm:justify-center"
      role="dialog"
      aria-modal="true"
      aria-labelledby="install-title"
    >
      <div class="w-full rounded-lg border border-border bg-background p-4 text-foreground shadow-xl sm:max-w-sm">
        <div class="mb-4 flex items-start gap-3">
          <div class="flex h-11 w-11 shrink-0 items-center justify-center rounded-md bg-primary/10">
            <Icon icon="lucide:download" class="text-2xl text-primary" />
          </div>
          <div class="min-w-0 flex-1">
            <h2 id="install-title" class="text-base font-semibold leading-6">Install SAT20 Wallet</h2>
            <p class="mt-1 text-sm leading-5 text-muted-foreground">{{ installMessage }}</p>
          </div>
        </div>

        <div v-if="installSteps.length" class="mb-4 rounded-md border border-border bg-muted/30 p-3">
          <div
            v-for="(step, index) in installSteps"
            :key="step"
            class="flex gap-3 text-sm leading-5"
            :class="index > 0 ? 'mt-2' : ''"
          >
            <span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-primary text-xs font-medium text-primary-foreground">
              {{ index + 1 }}
            </span>
            <span class="text-muted-foreground">{{ step }}</span>
          </div>
        </div>

        <div class="grid gap-2">
          <Button v-if="installPromptEvent" class="w-full" @click="promptInstall">
            Install
          </Button>
          <Button variant="outline" class="w-full" @click="showInstallPanel = false">
            Continue in browser
          </Button>
        </div>
      </div>
    </div>
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
import { computed, ref, onBeforeMount, onBeforeUnmount, onMounted, watch } from "vue";
import { useRouter } from "vue-router";
import { Icon } from "@iconify/vue";
import walletManager from "@/utils/sat20";
import Toaster from "@/components/ui/toast-new/Toaster.vue";
import Approve from "@/entrypoints/popup/pages/wallet/Approve.vue";
import { useGlobalStore, useWalletStore } from "@/store";
import { useAppVersion } from "@/composables/useAppVersion";
import { storeToRefs } from "pinia";
import { Button } from "@/components/ui/button";

type BeforeInstallPromptEvent = Event & {
  prompt: () => Promise<void>;
  userChoice: Promise<{ outcome: "accepted" | "dismissed"; platform: string }>;
};

const loading = ref(false);
const walletStore = useWalletStore();
const globalStore = useGlobalStore();
const router = useRouter();
const { checkForUpdates } = useAppVersion();
const { autoLockTime } = storeToRefs(globalStore);
let autoLockTimer: ReturnType<typeof setTimeout> | undefined;
const installPromptEvent = ref<BeforeInstallPromptEvent | undefined>();
const showInstallPanel = ref(false);

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

const isAndroid = () => {
  return /android/i.test(navigator.userAgent);
};

const isChrome = () => {
  const userAgent = navigator.userAgent;
  return /Chrome\//i.test(userAgent) && !/EdgA|Edg\//i.test(userAgent);
};

const isDesktop = () => {
  return !isIOS() && !isAndroid();
};

const installMessage = computed(() => {
  if (installPromptEvent.value) {
    if (isAndroid() && isChrome()) {
      return "Chrome installs the wallet through Google Play services. Make sure Google Play can connect before installing.";
    }
    return "Install the wallet as a standalone app on this device.";
  }
  if (isIOS()) {
    return "iPhone and iPad require adding the wallet from the browser share menu.";
  }
  if (isAndroid() && isChrome()) {
    return "Chrome may need Google Play services and Google Play network access to finish installation.";
  }
  if (isAndroid()) {
    return "If the install prompt is not shown, use the browser menu to add the wallet to your phone.";
  }
  return "If the install prompt is not shown, use the browser install icon or browser menu.";
});

const installSteps = computed(() => {
  if (installPromptEvent.value) {
    return isAndroid() && isChrome()
      ? ["Confirm Google Play services and Google Play are available.", "Tap Install.", "If no icon appears, check the Android app list for SAT20 Wallet."]
      : [];
  }

  if (isIOS()) {
    return ["Open this page in Safari.", "Tap Share.", "Choose Add to Home Screen."];
  }
  if (isAndroid() && isChrome()) {
    return ["Confirm Google Play services and Google Play can connect.", "Open Chrome menu.", "Choose Install app or Add to Home screen."];
  }
  if (isAndroid()) {
    return ["Open the browser menu.", "Choose Install app or Add to phone.", "Confirm the installation."];
  }
  if (isDesktop()) {
    return ["Use the install icon in the address bar.", "Or open the browser menu and choose Install app."];
  }
  return [];
});

const showInstallFallback = () => {
  if (isStandaloneApp()) return;
  showInstallPanel.value = true;
};

const promptInstall = async () => {
  if (isStandaloneApp()) return;

  if (!installPromptEvent.value) {
    showInstallPanel.value = true;
    showInstallFallback();
    return;
  }

  const promptEvent = installPromptEvent.value;
  installPromptEvent.value = undefined;
  await promptEvent.prompt();
  await promptEvent.userChoice.catch(() => undefined);
  showInstallPanel.value = false;
};

const onBeforeInstallPrompt = (event: Event) => {
  event.preventDefault();
  installPromptEvent.value = event as BeforeInstallPromptEvent;

  if (isInstallRequest()) {
    showInstallPanel.value = true;
  }
};

const onAppInstalled = () => {
  installPromptEvent.value = undefined;
  showInstallPanel.value = false;
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
    showInstallPanel.value = true;
  }
  window.addEventListener("appinstalled", onAppInstalled);

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
  window.removeEventListener("appinstalled", onAppInstalled);
  activityEvents.forEach((eventName) => {
    window.removeEventListener(eventName, resetAutoLockTimer);
  });
});
</script>
