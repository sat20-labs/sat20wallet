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
        <span class="text-sm font-medium">已绑定推荐人：</span>
        <div class="flex items-center gap-2 px-2 py-1 bg-green-500/20 rounded">
          <span class="text-sm">{{ boundReferrer }}</span>
          <!-- 如果有txId，显示mempool图标 -->
          <button 
            v-if="boundReferrerTxId" 
            @click="handleBoundReferrerMempoolClick()"
            class="ml-1 p-0.5 hover:bg-green-500/30 rounded transition-colors"
            :title="`查看绑定交易`"
          >
            <Icon icon="lucide:external-link" class="w-3 h-3 text-green-300 hover:text-green-100" />
          </button>
        </div>
      </div>

      <!-- 显示已注册的推荐人 -->
      <div class="text-red-400 flex flex-col gap-2" v-if="referrerNames.length && !isLoading">
        <span class="text-sm font-medium">已注册推荐人：</span>
        <div class="flex flex-wrap gap-2">
          <div v-for="name in referrerNames" :key="name"
            class="inline-flex items-center gap-1 px-2 py-1 bg-red-500/20 rounded text-xs">
            <span>{{ name }}</span>
            <!-- 如果有txId，显示mempool图标 -->
            <button 
              v-if="referrerTxIds[name]" 
              @click="handleMempoolClick(name)"
              class="ml-1 p-0.5 hover:bg-red-500/30 rounded transition-colors"
              :title="`查看 ${name} 的注册交易`"
            >
              <Icon icon="lucide:external-link" class="w-3 h-3 text-red-300 hover:text-red-100" />
            </button>
          </div>
        </div>
      </div>

      <div class="flex justify-center gap-3 mb-2" v-if="!isLoading">
        <Button :disabled="referrerNames.length || isLoading" variant="secondary" class="h-10 w-32" @click="handleRegisterClick"
          :loading="isLoading">
          注册推荐人
        </Button>
        <Button :disabled="!!boundReferrer || isLoading" :variant="boundReferrer ? 'outline' : 'default'"
          :class="boundReferrer || isLoading ? 'opacity-50 cursor-not-allowed bg-gray-600' : ''"
          @click="handleBindClick" class="h-10 w-32" :loading="isLoading">
          {{ boundReferrer ? '已绑定' : '绑定推荐人' }}
        </Button>
      </div>

      <!-- 铸造域名链接 -->
      <div class="flex justify-center mb-2">
        <a href="https://app.ordx.market/inscribe/" target="_blank" rel="noopener noreferrer"
          class="text-primary hover:text-primary/80 underline">
          {{ $t('referrerManagement.inscribeDomain') }}
        </a>
      </div>

      <!-- 当已绑定推荐人时显示提示 -->
      <div v-if="boundReferrer && !isLoading" class="text-center">
        <p class="text-sm text-muted-foreground">
          已绑定推荐人，不能重复绑定
        </p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button } from '@/components/ui/button'
import { Icon } from '@iconify/vue'
import { useWalletStore } from '@/store/wallet'
import { storeToRefs } from 'pinia'
import satsnetStp from '@/utils/stp'
import { useGlobalStore } from '@/store/global'
import { getConfig } from '@/config/wasm'
import { useRouter } from 'vue-router'
import { useReferrerManager } from '@/composables/useReferrerManager'
import satnetApi from '@/apis/satnet'
import { Network } from '@/types'
import { generateMempoolUrl } from '@/utils'

const { t } = useI18n()
const isExpanded = ref(false)
const isLoading = ref(false)
const walletStore = useWalletStore()
const { publicKey, address } = storeToRefs(walletStore)
const referrerNames = ref<string[]>([])
const boundReferrer = ref<string | null>(null)
const referrerTxIds = ref<Record<string, string>>({})
const boundReferrerTxId = ref<string | null>(null)

const globalStore = useGlobalStore()
const { env } = storeToRefs(globalStore)
const { network } = storeToRefs(walletStore)
const router = useRouter()

const { getLocalReferrerNames, getLocalBoundReferrer, getAllReferrerTxIds, getLocalBoundReferrerTxId, cleanInvalidReferrerCache, clearReferrerNameCache, removeLocalBoundReferrer, removeBoundReferrerTxId } = useReferrerManager()

function handleRegisterClick() {
  router.push('/wallet/setting/referrer/register')
}

function handleBindClick() {
  if (boundReferrer.value) {
    // 如果已绑定，跳转到绑定页面进行解除绑定操作
    router.push('/wallet/setting/referrer/bind')
  } else {
    // 如果未绑定，跳转到绑定页面进行绑定操作
    router.push('/wallet/setting/referrer/bind')
  }
}

// 处理点击mempool图标
function handleMempoolClick(referrerName: string) {
  const txId = referrerTxIds.value[referrerName]
  if (txId) {
    // 使用generateMempoolUrl生成mempool链接
    const mempoolUrl = generateMempoolUrl({
      network: network.value,
      path: `tx/${txId}`,
    })
    
    // 在新标签页中打开mempool链接
    window.open(mempoolUrl, '_blank', 'noopener,noreferrer')
  }
}

// 处理点击绑定推荐人的mempool图标
function handleBoundReferrerMempoolClick() {
  if (boundReferrerTxId.value) {
    // 使用generateMempoolUrl生成mempool链接
    const mempoolUrl = generateMempoolUrl({
      network: network.value,
      path: `tx/${boundReferrerTxId.value}`,
    })
    
    // 在新标签页中打开mempool链接
    window.open(mempoolUrl, '_blank', 'noopener,noreferrer')
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
    // 首先检查本地存储的绑定推荐人
    const localBoundReferrer = await getLocalBoundReferrer(address.value)
    const localBoundReferrerTxId = await getLocalBoundReferrerTxId(address.value)
    
    // 只有当本地有绑定推荐人且有对应的txId时，才认为绑定有效
    if (localBoundReferrer && localBoundReferrerTxId) {
      boundReferrer.value = localBoundReferrer
      console.log('本地绑定的推荐人:', boundReferrer.value)
      return
    } else if (localBoundReferrer && !localBoundReferrerTxId) {
      // 如果本地有绑定推荐人但没有txId，说明绑定无效，清除本地缓存
      console.log('本地绑定推荐人无效（无txId），清除缓存:', localBoundReferrer)
      await removeLocalBoundReferrer(address.value)
      await removeBoundReferrerTxId(address.value)
    }

    // 如果本地没有有效绑定，则从服务器获取
    const networkType = network.value === Network.LIVENET ? 'livenet' : 'testnet'
    console.log('获取绑定推荐人，地址:', address.value, '网络:', networkType)

    const response = await satnetApi.getReferrerByAddress({
      address: address.value,
      network: networkType
    })

    console.log('ordx API 响应:', response)

    if (response && response.code === 0 && response.referrer) {
      boundReferrer.value = response.referrer
      console.log('服务器绑定的推荐人:', boundReferrer.value)
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

  try {
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
        // 服务器请求失败，使用本地数据，但需要清理无效缓存
        await cleanInvalidReferrerCache(address.value)
        const validLocalNames = await getLocalReferrerNames(address.value)
        referrerNames.value = validLocalNames
      } else {
        const serverNames = res?.names || []
        if (serverNames.length > 0) {
          // 服务器有数据，使用服务器数据，并清除本地缓存中与服务器数据重复的名字
          referrerNames.value = serverNames
          
          // 清除本地缓存中与服务器数据重复的名字
          for (const serverName of serverNames) {
            if (localNames.includes(serverName)) {
              await clearReferrerNameCache(address.value, serverName)
              console.log(`已清除与服务器重复的推荐人缓存: ${serverName}`)
            }
          }
        } else {
          // 服务器没有数据，使用本地数据，但需要清理无效缓存
          await cleanInvalidReferrerCache(address.value)
          const validLocalNames = await getLocalReferrerNames(address.value)
          referrerNames.value = validLocalNames
        }
      }
    } else {
      console.warn('未能获取serverPubKey，使用本地数据，但需要清理无效缓存')
      await cleanInvalidReferrerCache(address.value)
      const validLocalNames = await getLocalReferrerNames(address.value)
      referrerNames.value = validLocalNames
    }

    console.log('最终使用的推荐人名字:', referrerNames.value)
  } catch (error) {
    console.error('加载推荐人名字失败:', error)
    referrerNames.value = []
  }
}

// 加载推荐人的txId映射
async function loadReferrerTxIds() {
  if (!address.value) return

  try {
    const txIds = await getAllReferrerTxIds(address.value)
    referrerTxIds.value = txIds
    console.log('推荐人txId映射:', txIds)
  } catch (error) {
    console.error('加载推荐人txId失败:', error)
    referrerTxIds.value = {}
  }
}

// 加载绑定推荐人的txId
async function loadBoundReferrerTxId() {
  if (!address.value) return

  try {
    const txId = await getLocalBoundReferrerTxId(address.value)
    boundReferrerTxId.value = txId
    console.log('绑定推荐人txId:', txId)
  } catch (error) {
    console.error('加载绑定推荐人txId失败:', error)
    boundReferrerTxId.value = null
  }
}

// 加载所有推荐人相关信息
async function loadAllReferrerInfo() {
  isLoading.value = true
  try {
    await Promise.all([
      loadReferrerNames(),
      loadBoundReferrer(),
      loadReferrerTxIds(),
      loadBoundReferrerTxId()
    ])
  } catch (error) {
    console.error('加载推荐人信息失败:', error)
  } finally {
    isLoading.value = false
  }
}

watch([publicKey, isExpanded], ([addr, expanded]) => {
  if (expanded) loadAllReferrerInfo()
})

onMounted(() => {
  if (isExpanded.value) loadAllReferrerInfo()
})
</script>