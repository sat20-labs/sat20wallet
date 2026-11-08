<template>
  <div class="w-full px-2 bg-zinc-700/40 rounded-lg">
    <button @click="isExpanded = !isExpanded"
      class="flex items-center justify-between w-full p-2 text-left text-primary font-medium rounded-lg">
      <div>
        <h2 class="text-lg font-bold text-zinc-200">{{ $t('referrerManagement.title') }}</h2>
        <p class="text-muted-foreground">{{ $t('referrerManagement.description') }}</p>
      </div>
      <div class="mr-2">
        <Icon v-if="isExpanded" icon="lucide:chevrons-up" class="mr-2 h-4 w-4" />
        <Icon v-else icon="lucide:chevrons-down" class="mr-2 h-4 w-4" />
      </div>
    </button>
    <div v-if="isExpanded" class="space-y-6 px-2 py-2 mb-2">
      <!-- Loading状态显示 -->
      <div v-if="isLoading" class="text-center py-4">
        <div class="flex items-center justify-center gap-2">
          <div class="animate-spin rounded-full h-4 w-4 border-b-2 border-primary"></div>
          <span class="text-sm text-muted-foreground">{{ t('referrerManagement.loadingReferrerInfo') }}</span>
        </div>
      </div>

      <!-- 显示绑定的推荐人 -->
      <div class="text-green-400 flex flex-col gap-2" v-if="boundReferrer && !isLoading">
        <span class="text-sm font-medium">{{ t('settingsUi.boundReferrerLabel') }}</span>
        <div class="flex items-center gap-2 px-2 py-1 bg-green-500/20 rounded">
          <span class="text-sm">{{ boundReferrer }}</span>
          <!-- 如果有txId，显示mempool图标 -->
          <button
            v-if="boundReferrerTxId && network !== Network.REGTEST"
            @click="handleBoundReferrerMempoolClick()"
            class="ml-1 p-0.5 hover:bg-green-500/30 rounded transition-colors"
            :title="t('settingsUi.viewBinding')"
          >
            <Icon icon="lucide:external-link" class="w-3 h-3 text-green-300 hover:text-green-100" />
          </button>
        </div>
      </div>

      <!-- 显示已注册的推荐人 -->
      <div class="text-red-400 flex flex-col gap-2" v-if="referrerNames.length && !isLoading">
        <span class="text-sm font-medium">{{ t('settingsUi.registeredReferrer') }}</span>
        <div class="flex flex-wrap gap-2">
          <div v-for="name in referrerNames" :key="name"
            class="inline-flex items-center gap-1 px-2 py-1 bg-red-500/20 rounded text-xs">
            <span>{{ name }}</span>
          </div>
        </div>
      </div>

      <div class="flex justify-center gap-3 mb-2" v-if="!isLoading">
        <Button :disabled="!canRegister" variant="secondary" class="h-10 w-32" @click="handleRegisterClick"
          :loading="isLoading">
          {{ t('settingsUi.registerReferrer') }}
        </Button>
        <Button :disabled="!canBind" :variant="boundReferrer ? 'outline' : 'default'"
          :class="!canBind ? 'opacity-50 cursor-not-allowed bg-gray-600' : ''"
          @click="handleBindClick" class="h-10 w-32" :loading="isLoading">
          {{ boundReferrer ? t('referrerManagement.alreadyBound') : t('settingsUi.bindReferrer') }}
        </Button>
      </div>

      <p v-if="!isLoading && (!bindingKnown || !registrationKnown)" role="alert" class="text-sm text-red-400">
        {{ t('referrerManagement.statusLookupFailed') }}
      </p>

      <!-- 铸造域名链接 -->
      <div v-if="network !== Network.REGTEST" class="flex justify-center mb-2">
        <a href="https://app.ordx.market/inscribe/" target="_blank" rel="noopener noreferrer"
          class="text-primary hover:text-primary/80 underline">
          {{ $t('referrerManagement.inscribeDomain') }}
        </a>
      </div>

      <!-- 当已绑定推荐人时显示提示 -->
      <div v-if="boundReferrer && !isLoading" class="text-center">
        <p class="text-sm text-muted-foreground">
          {{ t('settingsUi.cannotRebind') }}
        </p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button } from '@/components/ui/button'
import { Icon } from '@iconify/vue'
import { useWalletStore } from '@/store/wallet'
import { openMempoolLink } from '@/utils/browser'
import { storeToRefs } from 'pinia'
import walletManager from '@/utils/sat20'
import { useGlobalStore } from '@/store/global'
import { getConfig } from '@/config/wasm'
import { useRouter } from 'vue-router'
import { useReferrerManager } from '@/composables/useReferrerManager'
import satnetApi from '@/apis/satnet'
import { Network } from '@/types'

const { t } = useI18n()
const isExpanded = ref(false)
const isLoading = ref(false)
const walletStore = useWalletStore()
const { publicKey, address } = storeToRefs(walletStore)
const referrerNames = ref<string[]>([])
const boundReferrer = ref<string | null>(null)
const boundReferrerTxId = ref<string | null>(null)
const bindingKnown = ref(false)
const registrationKnown = ref(false)
const canBind = computed(() => !isLoading.value && bindingKnown.value && !boundReferrer.value)
const canRegister = computed(() => !isLoading.value && registrationKnown.value && !referrerNames.value.length)

const globalStore = useGlobalStore()
const { env } = storeToRefs(globalStore)
const { network } = storeToRefs(walletStore)
const router = useRouter()

const { getLocalBoundReferrer, getLocalBoundReferrerTxId } = useReferrerManager()
const statusContext = () => [env.value, network.value, address.value, publicKey.value].join('|')

function handleRegisterClick() {
  if (!canRegister.value) return
  router.push('/wallet/setting/referrer/register')
}

function handleBindClick() {
  if (!canBind.value) return
  router.push('/wallet/setting/referrer/bind')
}


// 处理点击绑定推荐人的mempool图标
async function handleBoundReferrerMempoolClick() {
  if (boundReferrerTxId.value) {
    try {
      // 使用统一的 mempool 链接打开函数
      await openMempoolLink(network.value, `tx/${boundReferrerTxId.value}`)
    } catch (error) {
      console.error('打开 mempool 链接失败:', error)
    }
  }
}



function getServerPubKey() {
  // 获取配置中的第一个Peer的公钥，和bind.vue保持一致
  const config = getConfig(env.value, network.value)
  // 取Peers[1]，如无则取Peers[0]
  const peer = config.Peers[1] || config.Peers[0]
  if (peer) {
    const parts = peer.split('@')
    if (parts.length >= 2) {
      return parts[1]
    }
  }
  return ''
}

// Only a verified response can enable a new action. A query failure is not
// evidence that the account is unregistered or unbound.
async function loadBoundReferrer() {
  const context = statusContext()
  const targetAddress = address.value
  bindingKnown.value = false
  if (!targetAddress) return
  try {
    const localName = await getLocalBoundReferrer(targetAddress)
    const localTxId = await getLocalBoundReferrerTxId(targetAddress)
    if (context !== statusContext()) return
    if (localName && localTxId) {
      boundReferrer.value = localName
      boundReferrerTxId.value = localTxId
    }
    const response = await satnetApi.getReferrerByAddress({ address: targetAddress, network: network.value })
    if (context !== statusContext()) return
    if (response?.code === 0 && typeof response.referrer?.Name === 'string' && response.referrer.Name.trim()) {
      boundReferrer.value = response.referrer.Name
    } else if (response?.code === -1 && response.msg === 'key not found') {
      // A locally submitted binding remains unavailable for duplicate binding
      // while its transaction is waiting for confirmation.
      if (!localName || !localTxId) boundReferrer.value = null
    } else {
      throw new Error(t('settingsUi.unverifiedBinding'))
    }
    bindingKnown.value = true
  } catch (error) {
    console.error('获取绑定推荐人失败:', error)
  }
}

async function loadReferrerNames() {
  const context = statusContext()
  registrationKnown.value = false
  if (!address.value) return
  try {
    const serverPubKey = getServerPubKey()
    if (!serverPubKey) throw new Error(t('settingsUi.referrerUnavailable'))
    const [err, res] = await walletManager.getAllRegisteredReferrerName(serverPubKey)
    if (context !== statusContext()) return
    // The existing WASM API encodes a successful empty list as names:null.
    if (err || !res || !('names' in res) || (res.names !== null &&
      (!Array.isArray(res.names) || !res.names.every(name => typeof name === 'string' && name.trim())))) {
      throw err || new Error(t('settingsUi.unverifiedRegistration'))
    }
    referrerNames.value = res.names || []
    registrationKnown.value = true
  } catch (error) {
    console.error('加载推荐人名字失败:', error)
  }
}

async function loadAllReferrerInfo() {
  const context = statusContext()
  isLoading.value = true
  bindingKnown.value = false
  registrationKnown.value = false
  boundReferrer.value = null
  boundReferrerTxId.value = null
  referrerNames.value = []
  try {
    await Promise.all([loadReferrerNames(), loadBoundReferrer()])
  } finally {
    if (context === statusContext()) isLoading.value = false
  }
}

watch([publicKey, address, network, env, isExpanded], () => {
  bindingKnown.value = false
  registrationKnown.value = false
  if (isExpanded.value) loadAllReferrerInfo()
})
</script>
