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
      <!-- 显示绑定的推荐人 -->
      <div class="text-green-400 flex flex-col gap-2" v-if="boundReferrer">
        <span class="text-sm font-medium">已绑定推荐人：</span>
        <div class="flex items-center gap-2 px-2 py-1 bg-green-500/20 rounded">
          <span class="text-sm">{{ boundReferrer }}</span>
        </div>
      </div>

      <!-- 显示已注册的推荐人 -->
      <div class="text-red-400 flex flex-col gap-2" v-if="referrerNames.length">
        <span class="text-sm font-medium">已注册推荐人：</span>
        <div class="flex flex-wrap gap-2">
          <div v-for="name in referrerNames" :key="name"
            class="inline-flex items-center gap-1 px-2 py-1 bg-red-500/20 rounded text-xs">
            <span>{{ name }}</span>
          </div>
        </div>
      </div>

      <div class="flex justify-center gap-3 mb-2">
        <Button :disabled="referrerNames.length > 0" variant="secondary" class="h-10 w-32" @click="handleRegisterClick">
          {{ $t('referrerManagement.registerAsReferrer') }}
        </Button>
        <Button as-child class="h-10 w-32">
          <RouterLink to="/wallet/setting/referrer/bind" class="w-full">
            {{ $t('referrerManagement.bindReferrer') }}
          </RouterLink>
        </Button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, onMounted } from 'vue'
import { Button } from '@/components/ui/button'
import { useWalletStore } from '@/store/wallet'
import { storeToRefs } from 'pinia'
import satsnetStp from '@/utils/stp'
import { useGlobalStore } from '@/store/global'
import { getConfig } from '@/config/wasm'
import { useRouter } from 'vue-router'
import { useReferrerManager } from '@/composables/useReferrerManager'
import satnetApi from '@/apis/satnet'
import { Network } from '@/types'

const isExpanded = ref(false)
const walletStore = useWalletStore()
const { publicKey, address } = storeToRefs(walletStore)
const referrerNames = ref<string[]>([])
const boundReferrer = ref<string | null>(null)

const globalStore = useGlobalStore()
const { env } = storeToRefs(globalStore)
const { network } = storeToRefs(walletStore)
const router = useRouter()

const { getLocalReferrerNames } = useReferrerManager()

function handleRegisterClick() {
  if (referrerNames.value.length === 0) {
    router.push('/wallet/setting/referrer/register')
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

// 获取绑定的推荐人信息
async function loadBoundReferrer() {
  if (!address.value) return

  try {
    const networkType = network.value === Network.LIVENET ? 'livenet' : 'testnet'
    console.log('获取绑定推荐人，地址:', address.value, '网络:', networkType)

    const response = await satnetApi.getReferrerByAddress({
      address: address.value,
      network: networkType
    })

    console.log('ordx API 响应:', response)

    if (response && response.code === 0 && response.referrer) {
      boundReferrer.value = response.referrer
      console.log('绑定的推荐人:', boundReferrer.value)
    } else {
      boundReferrer.value = null
      console.log('未绑定推荐人')
    }
  } catch (error) {
    console.error('获取绑定推荐人失败:', error)
    boundReferrer.value = null
  }
}

async function loadReferrerNames() {
  if (!address.value) return

  // 先获取本地存储的推荐人名字
  const localNames = await getLocalReferrerNames(address.value)
  console.log('本地推荐人名字:', localNames)

  // 从服务器获取推荐人名字
  const serverPubKey = getServerPubKey()
  console.log('serverPubKey', serverPubKey)

  if (serverPubKey) {
    const [err, res] = await satsnetStp.getAllRegisteredReferrerName(serverPubKey)
    console.log('服务器返回:', res)

    if (err) {
      console.error('获取推荐人失败', err)
      // 服务器请求失败，使用本地数据
      referrerNames.value = localNames
    } else {
      const serverNames = res?.names || []
      // 如果服务器有数据，使用服务器数据；否则使用本地数据
      referrerNames.value = serverNames.length > 0 ? serverNames : localNames
    }
  } else {
    console.warn('未能获取serverPubKey，使用本地数据')
    referrerNames.value = localNames
  }

  console.log('最终使用的推荐人名字:', referrerNames.value)
}

// 加载所有推荐人相关信息
async function loadAllReferrerInfo() {
  await Promise.all([
    loadReferrerNames(),
    loadBoundReferrer()
  ])
}

watch([publicKey, isExpanded], ([addr, expanded]) => {
  if (expanded) loadAllReferrerInfo()
})

onMounted(() => {
  if (isExpanded.value) loadAllReferrerInfo()
})
</script>