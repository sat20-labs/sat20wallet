<template>
  <div class="flex h-screen w-full flex-col bg-background text-foreground">
    <header class="flex h-14 shrink-0 items-center gap-2 border-b border-border px-3">
      <Button size="icon" variant="ghost" aria-label="Back" @click="goBack">
        <Icon icon="lucide:chevron-left" class="text-lg" />
      </Button>
      <div class="min-w-0 flex-1">
        <p class="truncate text-sm font-medium">SAT20 Market</p>
        <p class="truncate text-xs text-muted-foreground">{{ displayUrl }}</p>
      </div>
      <Button size="icon" variant="ghost" aria-label="Reload" @click="reload">
        <Icon icon="lucide:rotate-cw" class="text-lg" />
      </Button>
      <Button size="icon" variant="ghost" aria-label="Home" @click="loadHome">
        <Icon icon="lucide:home" class="text-lg" />
      </Button>
    </header>

    <div class="relative min-h-0 flex-1">
      <iframe
        ref="frameRef"
        :key="frameKey"
        :src="frameUrl"
        class="h-full w-full border-0 bg-background"
        allow="clipboard-read; clipboard-write"
        title="SAT20 Market"
        @load="handleLoad"
        @error="handleError"
      />

      <div
        v-if="loading"
        class="absolute inset-0 flex items-center justify-center bg-background/90"
      >
        <div class="text-center">
          <Icon icon="lucide:loader-2" class="mx-auto mb-2 text-2xl animate-spin" />
          <p class="text-sm text-muted-foreground">Loading Market</p>
        </div>
      </div>

      <div
        v-if="!isOnline || loadError"
        class="absolute inset-0 flex items-center justify-center bg-background p-6"
      >
        <div class="w-full max-w-sm text-center">
          <Icon icon="lucide:cloud-off" class="mx-auto mb-3 text-3xl text-muted-foreground" />
          <h2 class="mb-2 text-lg font-semibold">{{ errorTitle }}</h2>
          <p class="mb-4 text-sm text-muted-foreground">{{ errorMessage }}</p>
          <Button class="w-full" @click="reload">Retry</Button>
        </div>
      </div>

      <div
        v-if="pendingRequests > 0"
        class="pointer-events-none absolute bottom-4 left-1/2 -translate-x-1/2 rounded-md border border-border bg-background px-3 py-2 text-xs text-muted-foreground shadow"
      >
        Waiting for wallet confirmation
      </div>
    </div>

    <NavFooter />
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Icon } from '@iconify/vue'
import { Button } from '@/components/ui/button'
import NavFooter from '@/components/layout/NavFooter.vue'
import { usePwaDappBridge } from '@/composables/usePwaDappBridge'
import { useWalletStore } from '@/store'
import { Network } from '@/types'
import { SAT20_DAPP_PROTOCOL } from '@/types/sat20-dapp-connect'
import { addAuthorizedOrigin } from '@/lib/authorized-origins'

const DEFAULT_MARKET_URL = import.meta.env.DEV
  ? `${window.location.protocol}//${window.location.hostname}:3006`
  : 'https://satsnet.ordx.market'

const router = useRouter()
const walletStore = useWalletStore()

const resolveMarketUrl = () => {
  const baseUrl = import.meta.env.VITE_SAT20_MARKET_URL || DEFAULT_MARKET_URL
  const network = walletStore.network === Network.TESTNET ? 'testnet' : 'mainnet'

  try {
    const url = new URL(baseUrl)
    url.searchParams.set('network', network)
    return url.href
  } catch {
    const separator = baseUrl.includes('?') ? '&' : '?'
    return `${baseUrl}${separator}network=${network}`
  }
}

const normalizeDappUrl = (urlValue: string) => {
  const network = walletStore.network === Network.TESTNET ? 'testnet' : 'mainnet'

  try {
    const url = new URL(urlValue, frameUrl.value)
    url.searchParams.set('network', network)
    return url.href
  } catch {
    return urlValue
  }
}

const frameRef = ref<HTMLIFrameElement | null>(null)
const frameKey = ref(0)
const frameUrl = ref(resolveMarketUrl())
const loading = ref(true)
const loadError = ref(false)
const isOnline = ref(navigator.onLine)
const activeDappOrigin = ref('')
const activeDappUrl = ref(frameUrl.value)

const displayUrl = computed(() => {
  try {
    return new URL(activeDappUrl.value || frameUrl.value).host
  } catch {
    return activeDappUrl.value || frameUrl.value
  }
})

const errorTitle = computed(() => isOnline.value ? 'Market failed to load' : 'Network unavailable')
const errorMessage = computed(() => isOnline.value
  ? 'Check the Market frame policy or try again.'
  : 'The wallet is still available offline, but Market needs a network connection.'
)

const bridge = usePwaDappBridge(
  () => frameRef.value?.contentWindow ?? null,
  () => activeDappUrl.value || frameUrl.value
)

const { pendingRequests } = bridge

const targetOrigin = () => {
  try {
    return new URL(frameUrl.value).origin
  } catch {
    return '*'
  }
}

const authorizeEmbeddedOrigin = async (origin: string) => {
  if (bridge.isAllowedOrigin(origin)) {
    await addAuthorizedOrigin(origin)
  }
}

const announceWalletReady = async (origin = targetOrigin()) => {
  activeDappOrigin.value = origin
  await authorizeEmbeddedOrigin(origin)
  bridge.announceReady(origin, {
    network: walletStore.network,
    accounts: walletStore.address ? [walletStore.address] : [],
    publicKey: walletStore.publicKey,
  })
}

const handleDappNavigate = (event: MessageEvent, message: Record<string, unknown>) => {
  if (event.source !== frameRef.value?.contentWindow) {
    return
  }
  if (!bridge.isAllowedOrigin(event.origin)) {
    return
  }
  if (message.origin && message.origin !== event.origin) {
    return
  }
  if (typeof message.href !== 'string') {
    return
  }

  try {
    const href = new URL(message.href)
    if (!bridge.isAllowedOrigin(href.origin)) {
      return
    }

    const normalizedHref = normalizeDappUrl(href.href)
    frameUrl.value = normalizedHref
    activeDappUrl.value = normalizedHref
    activeDappOrigin.value = href.origin
    loading.value = true
    loadError.value = false
    frameKey.value += 1
  } catch (error) {
    console.warn('Ignored invalid DApp navigation request:', error)
  }
}

const handleLoad = async () => {
  loading.value = false
  loadError.value = false
  await announceWalletReady()
}

const handleClientReady = async (event: MessageEvent) => {
  const message = event.data
  if (!message || message.protocol !== SAT20_DAPP_PROTOCOL) {
    return
  }
  if (message.type === 'SAT20_DAPP_NAVIGATE') {
    handleDappNavigate(event, message)
    return
  }
  if (message.type !== 'SAT20_DAPP_CLIENT_READY') {
    return
  }
  if (event.source !== frameRef.value?.contentWindow) {
    return
  }
  if (!bridge.isAllowedOrigin(event.origin)) {
    return
  }
  if (message.origin && message.origin !== event.origin) {
    return
  }

  if (typeof message.href === 'string') {
    try {
      const href = new URL(message.href)
      if (href.origin === event.origin) {
        activeDappUrl.value = href.href
      }
    } catch {
      activeDappUrl.value = event.origin
    }
  } else {
    activeDappUrl.value = event.origin
  }
  await announceWalletReady(event.origin)
}

const handleError = () => {
  loading.value = false
  loadError.value = true
}

const reload = () => {
  loading.value = true
  loadError.value = false
  try {
    frameRef.value?.contentWindow?.location.reload()
  } catch (error) {
    console.warn('Failed to reload active DApp frame, reloading active DApp URL:', error)
    frameUrl.value = activeDappUrl.value || frameUrl.value
    activeDappOrigin.value = targetOrigin()
    frameKey.value += 1
  }
}

const loadHome = () => {
  frameUrl.value = resolveMarketUrl()
  activeDappUrl.value = frameUrl.value
  activeDappOrigin.value = targetOrigin()
  loading.value = true
  loadError.value = false
  frameKey.value += 1
}

const goBack = () => {
  router.push('/wallet')
}

const updateOnlineState = () => {
  isOnline.value = navigator.onLine
}

watch(
  () => [walletStore.address, walletStore.publicKey],
  ([address, publicKey]) => {
    bridge.announceEvent('accountChanged', activeDappOrigin.value || targetOrigin(), {
      accounts: address ? [address] : [],
      publicKey,
    })
  }
)

watch(
  () => walletStore.network,
  (network) => {
    bridge.announceEvent('networkChanged', activeDappOrigin.value || targetOrigin(), { network })
  }
)

watch(
  () => walletStore.locked,
  (locked) => {
    if (locked) {
      bridge.announceEvent('disconnect', activeDappOrigin.value || targetOrigin(), { reason: 'wallet_locked' })
    }
  }
)

onMounted(() => {
  activeDappOrigin.value = targetOrigin()
  bridge.start()
  window.addEventListener('message', handleClientReady)
  window.addEventListener('online', updateOnlineState)
  window.addEventListener('offline', updateOnlineState)
})

onBeforeUnmount(() => {
  bridge.stop()
  window.removeEventListener('message', handleClientReady)
  window.removeEventListener('online', updateOnlineState)
  window.removeEventListener('offline', updateOnlineState)
})
</script>
