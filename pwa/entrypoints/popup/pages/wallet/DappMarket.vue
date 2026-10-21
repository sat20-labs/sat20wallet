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
      <Button v-if="!embeddedMarket" size="sm" variant="outline" :disabled="disconnecting" aria-label="Disconnect DApp" @click="disconnectDapp">
        <Icon icon="lucide:unplug" class="mr-1 text-sm" aria-hidden="true" />
        Disconnect
      </Button>
    </header>

    <div class="relative min-h-0 flex-1">
      <iframe
        ref="frameRef"
        :key="frameKey"
        v-if="frameUrl"
        :src="frameUrl"
        class="h-full w-full border-0 bg-background"
        allow="clipboard-read; clipboard-write"
        title="SAT20 Market"
        @load="handleLoad"
        @error="handleError"
      />

      <div
        v-if="frameUrl && loading"
        class="absolute inset-0 flex items-center justify-center bg-background/90"
      >
        <div class="text-center">
          <Icon icon="lucide:loader-2" class="mx-auto mb-2 text-2xl animate-spin" />
          <p class="text-sm text-muted-foreground">Loading Market</p>
        </div>
      </div>

      <div
        v-if="!frameUrl || !isOnline || loadError"
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
import { isRegtestBuild } from '@/utils/networkMode'
import { MarketFrameReady } from '@/utils/marketFrameReady'
import { SAT20_DAPP_PROTOCOL } from '@/types/sat20-dapp-connect'
import { createDappGrant, getCurrentDappScope, revokeDappGrant } from '@/lib/authorized-origins'
import { getEmbeddedMarketCapabilities } from '@/lib/dapp-origin-policy'
import { assertWalletIdentityReady } from '@/lib/identity-boundary'
import { assertNoMnemonicView } from '@/lib/sensitive-session'
import { walletStorage } from '@/lib/walletStorage'
import {
  consumePendingDappIdentityDisconnect,
  registerReadyDappIdentitySink,
} from '@/lib/dapp-identity-events'

const DEFAULT_MARKET_URL = import.meta.env.DEV
  ? `${window.location.protocol}//${window.location.hostname}:3006/swap/`
  : 'https://satsnet.ordx.market/swap/'
const DEFAULT_TESTNET_MARKET_URL = import.meta.env.DEV
  ? DEFAULT_MARKET_URL
  : 'https://test-satsnet.ordx.market/swap/'

const router = useRouter()
const walletStore = useWalletStore()
const originPolicy = {
  development: import.meta.env.DEV,
  test: import.meta.env.MODE === 'test',
  regtest: isRegtestBuild,
  configured: import.meta.env.VITE_SAT20_DAPP_ALLOWED_ORIGINS,
  currentProtocol: window.location.protocol,
  currentHostname: window.location.hostname,
}

const resolveMarketUrl = () => {
  if (isRegtestBuild) {
    const configuredUrl = import.meta.env.VITE_SAT20_MARKET_URL
    if (!configuredUrl) return ''
    try {
      const url = new URL(configuredUrl)
      if (!['localhost', '127.0.0.1', '[::1]', '::1'].includes(url.hostname) || url.protocol !== 'http:') return ''
      url.searchParams.set('network', 'regtest')
      return url.href
    } catch {
      return ''
    }
  }
  const network = walletStore.network === Network.TESTNET ? 'testnet' : 'mainnet'
  const configuredUrl = import.meta.env.VITE_SAT20_MARKET_URL
  const baseUrl = configuredUrl || (network === 'testnet' ? DEFAULT_TESTNET_MARKET_URL : DEFAULT_MARKET_URL)

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
  const network = isRegtestBuild
    ? 'regtest'
    : walletStore.network === Network.TESTNET ? 'testnet' : 'mainnet'

  try {
    const url = new URL(urlValue, frameUrl.value)
    if (isRegtestBuild && (!['localhost', '127.0.0.1', '[::1]', '::1'].includes(url.hostname) || url.protocol !== 'http:')) return ''
    url.searchParams.set('network', network)
    return url.href
  } catch {
    return urlValue
  }
}

const frameRef = ref<HTMLIFrameElement | null>(null)
const frameKey = ref(0)
const frameUrl = ref(resolveMarketUrl())
const loading = ref(Boolean(frameUrl.value))
const loadError = ref(!frameUrl.value)
const readyTimedOut = ref(false)
const disconnecting = ref(false)
const readyGate = new MarketFrameReady((state) => {
  loading.value = state === 'loading'
  loadError.value = state === 'error' || state === 'timeout'
  readyTimedOut.value = state === 'timeout'
})
let navigationGeneration = 0
let unregisterIdentitySink: (() => void) | null = null
const deactivateIdentitySink = () => {
  unregisterIdentitySink?.()
  unregisterIdentitySink = null
}
const beginNavigation = () => {
  deactivateIdentitySink()
  navigationGeneration = readyGate.begin(frameUrl.value)
}
watch(frameRef, (frame) => readyGate.bind(navigationGeneration, frame?.contentWindow ?? null), { flush: 'post' })
const isOnline = ref(navigator.onLine)
const activeDappOrigin = ref('')
const activeDappUrl = ref(frameUrl.value)
const embeddedMarket = computed(() => {
  try {
    const origin = new URL(activeDappUrl.value || frameUrl.value).origin
    return getEmbeddedMarketCapabilities(origin, originPolicy) !== null
  } catch {
    return false
  }
})

const displayUrl = computed(() => {
  try {
    return new URL(activeDappUrl.value || frameUrl.value).host
  } catch {
    return activeDappUrl.value || frameUrl.value
  }
})

const errorTitle = computed(() => isOnline.value ? 'Market failed to load' : 'Network unavailable')
const errorMessage = computed(() => isOnline.value
  ? (!frameUrl.value
    ? 'No local regtest Market endpoint is configured.'
    : readyTimedOut.value ? 'Could not confirm Market is ready. Try again.' : 'Check the Market frame policy or try again.')
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
    return ''
  }
}

const announceWalletReady = (origin = targetOrigin(), payload: Record<string, unknown> = {}) => {
  if (!origin || !bridge.isAllowedOrigin(origin)) return
  activeDappOrigin.value = origin
  bridge.announceReady(origin, payload)
}

const handleDappNavigate = (event: MessageEvent, message: Record<string, unknown>) => {
  if (!readyGate.matchesSource(event.source) || event.source !== frameRef.value?.contentWindow) {
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
    if (!normalizedHref) return
    frameUrl.value = normalizedHref
    activeDappUrl.value = normalizedHref
    activeDappOrigin.value = href.origin
    beginNavigation()
    frameKey.value += 1
  } catch (error) {
    console.warn('Ignored invalid DApp navigation request:', error)
  }
}

const handleLoad = (event: Event) => {
  if (event.target !== frameRef.value || !readyGate.matchesSource(frameRef.value?.contentWindow ?? null)) return
  // DOM load is not application readiness (including browser error documents).
  if (!embeddedMarket.value) announceWalletReady()
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
  if (!readyGate.ready(event, bridge.isAllowedOrigin)) return
  activeDappUrl.value = message.href
  deactivateIdentitySink()
  if (consumePendingDappIdentityDisconnect(event.origin)) {
    bridge.announceEvent('disconnect', event.origin, { reason: 'wallet_scope_changed' })
  }
  const navigation = navigationGeneration
  const capabilities = getEmbeddedMarketCapabilities(event.origin, originPolicy)
  let payload: Record<string, unknown> = {}
  if (capabilities) {
    try {
      const scope = await getCurrentDappScope()
      const identity = walletStorage.getState()
      const assertCurrent = () => {
        assertNoMnemonicView()
        assertWalletIdentityReady(scope.identityGeneration)
        const state = walletStorage.getState()
        if (navigation !== navigationGeneration || !readyGate.matchesSource(event.source) ||
          event.source !== frameRef.value?.contentWindow || state.locked ||
          state.walletId !== identity.walletId || state.pubkey !== identity.pubkey ||
          state.address !== identity.address || state.network !== scope.network ||
          state.accountIndex !== scope.accountIndex || !state.address || !state.pubkey) {
          throw new Error('Embedded Market identity or frame changed')
        }
      }
      assertCurrent()
      const network = new URL(message.href).searchParams.get('network')
      if (network && network !== scope.network) throw new Error('Market network does not match wallet')
      const createdAt = Date.now()
      await createDappGrant({
        ...scope, origin: event.origin, capabilities: [...capabilities],
        createdAt, expiresAt: createdAt + 24 * 60 * 60_000, sessionOnly: true,
      }, assertCurrent)
      assertCurrent()
      payload = { network: scope.network, accounts: [identity.address], publicKey: identity.pubkey }
    } catch {
      // A lock, navigation or account switch must not publish the old identity.
      return
    }
  }
  announceWalletReady(event.origin, payload)
  unregisterIdentitySink = registerReadyDappIdentitySink(event.origin, (identityPayload) => {
    bridge.announceEvent('disconnect', event.origin, identityPayload)
  })
}

const handleError = (event: Event) => {
  if (event.target === frameRef.value && readyGate.matchesSource(frameRef.value?.contentWindow ?? null)) readyGate.error(navigationGeneration)
}

const reload = () => {
  if (!frameUrl.value) return
  frameUrl.value = activeDappUrl.value || frameUrl.value
  activeDappOrigin.value = targetOrigin()
  beginNavigation()
  frameKey.value += 1
}

const loadHome = () => {
  frameUrl.value = resolveMarketUrl()
  if (!frameUrl.value) {
    activeDappUrl.value = ''
    activeDappOrigin.value = ''
    loadError.value = true
    loading.value = false
    return
  }
  activeDappUrl.value = frameUrl.value
  activeDappOrigin.value = targetOrigin()
  beginNavigation()
  frameKey.value += 1
}

const goBack = () => {
  router.push('/wallet')
}

const disconnectDapp = async () => {
  const origin = activeDappOrigin.value || targetOrigin()
  if (!origin || disconnecting.value) return
  disconnecting.value = true
  try {
    await revokeDappGrant(origin, await getCurrentDappScope())
    bridge.announceEvent('disconnect', origin, { reason: 'user_disconnected' })
  } finally {
    disconnecting.value = false
  }
}

const updateOnlineState = () => {
  isOnline.value = navigator.onLine
}

onMounted(() => {
  if (frameUrl.value) {
    beginNavigation()
    readyGate.bind(navigationGeneration, frameRef.value?.contentWindow ?? null)
  }
  activeDappOrigin.value = targetOrigin()
  bridge.start()
  window.addEventListener('message', handleClientReady)
  window.addEventListener('online', updateOnlineState)
  window.addEventListener('offline', updateOnlineState)
})

onBeforeUnmount(() => {
  deactivateIdentitySink()
  readyGate.dispose()
  bridge.stop()
  window.removeEventListener('message', handleClientReady)
  window.removeEventListener('online', updateOnlineState)
  window.removeEventListener('offline', updateOnlineState)
})
</script>
