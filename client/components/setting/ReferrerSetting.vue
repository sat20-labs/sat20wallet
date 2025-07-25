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
      <div class="text-red-400 flex" v-if="referrerNames.length">
        <span class="text-sm ">已注册推荐人：</span>
        <ul class="text-sm flex gap-2 flw-wrap">
          <li v-for="n in referrerNames" :key="n">{{ n }}</li>
        </ul>
      </div>
      <div class="flex justify-center gap-3 mb-2">
        <Button as-child variant="secondary" class="h-10 w-32">
          <RouterLink to="/wallet/setting/referrer/register" class="w-full">
            {{ $t('referrerManagement.registerAsReferrer') }}
          </RouterLink>
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

const isExpanded = ref(false)
const walletStore = useWalletStore()
const { publicKey } = storeToRefs(walletStore)
const referrerNames = ref<string[]>([])

const globalStore = useGlobalStore()
const { env } = storeToRefs(globalStore)
const { network } = storeToRefs(walletStore)

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

async function loadReferrerNames() {
  const serverPubKey = getServerPubKey()
  console.log('serverPubKey', serverPubKey);

  if (serverPubKey) {
    const [err, res] = await satsnetStp.getAllRegisteredReferrerName(serverPubKey)
    console.log('res', res);

    if (err) {
      referrerNames.value = []
      console.error('获取推荐人失败', err)
    } else {
      referrerNames.value = res?.names || []
    }
  } else {
    referrerNames.value = []
    console.warn('未能获取serverPubKey，无法查询推荐人')
  }
  console.log(referrerNames.value);
}

watch([publicKey, isExpanded], ([addr, expanded]) => {
  if (expanded) loadReferrerNames()
})
onMounted(() => {
  if (isExpanded.value) loadReferrerNames()
})
</script>