<template>
  <div class="w-full px-2  bg-zinc-700/40 rounded-lg">
    <button @click="isExpanded = !isExpanded"
      class="flex items-center justify-between w-full p-2 text-left text-primary font-medium rounded-lg">
      <div>
        <h2 class="text-lg font-bold text-zinc-200">{{ $t('nodeSetting.title') }}</h2>
        <p class="text-muted-foreground">{{ $t('nodeSetting.description') }}</p>
      </div>
      <div class="mr-2">
        <Icon v-if="isExpanded" icon="lucide:chevrons-up" class="mr-2 h-4 w-4" />
        <Icon v-else icon="lucide:chevrons-down" class="mr-2 h-4 w-4" />
      </div>
    </button>
    <div v-if="isExpanded" class="space-y-6 px-2 py-4">
      <div v-if="isLoading" class="text-xs text-muted-foreground">{{ $t('loading', '加载中...') }}</div>
      <div v-if="isError" class="text-xs text-destructive">{{ error?.message || '加载失败' }}</div>
      <div v-if="!isLoading && !isError" class="bg-zinc-800/60 rounded p-3 text-xs text-zinc-200 space-y-2">
        <template v-if="displayMinerInfo.ServerNode || tempStakeData">
          <div v-if="tempStakeData" class="text-center text-yellow-400 mb-2">
            <Icon icon="lucide:clock" class="w-4 h-4 inline mr-1" />
            节点质押已提交，等待后台处理中...
          </div>
          <div v-if="displayMinerInfo.ServerNode" class="flex justify-between items-center">
            <span class="text-muted-foreground break-keep">连接的节点：</span>
            <span class="font-mono text-xs break-all">{{ displayMinerInfo.ServerNode }}</span>
          </div>
          <div v-if="displayMinerInfo.ServerNode" class="flex justify-between items-center">
            <span class="text-muted-foreground break-keep">本地节点：</span>
            <span class="font-mono text-xs break-all">{{ publicKey }}</span>
          </div>
          <div v-if="displayMinerInfo.ServerNode" class="flex justify-between items-center">
            <span class="text-muted-foreground break-keep">通道地址：</span>
            <a :href="generateMempoolUrl({
              network: network,
              path: `address/${displayMinerInfo.ChannelAddr}`,
            })" target="_blank" class="font-mono text-xs text-blue-400 hover:text-blue-300 underline break-all">
              {{ displayMinerInfo.ChannelAddr }}
            </a>
          </div>
          <div v-if="displayMinerInfo.ServerNode" class="flex justify-between items-center">
            <span class="text-muted-foreground">质押资产：</span>
            <span class="font-mono text-xs">{{ displayMinerInfo.AssetName }}</span>
          </div>
          <div v-if="displayMinerInfo.ServerNode" class="flex justify-between items-center">
            <span class="text-muted-foreground">资产数量：</span>
            <span class="font-mono text-xs">{{ displayMinerInfo.AssetAmt }}</span>
          </div>
          <div v-if="displayMinerInfo.ServerNode && displayMinerInfo.AnchorTxId"
            class="flex justify-between items-center">
            <span class="text-muted-foreground">锚定交易ID：</span>
            <a :href="generateMempoolUrl({
              network: network,
              path: `tx/${displayMinerInfo.AnchorTxId}`,
            })" target="_blank" class="font-mono text-xs text-blue-400 hover:text-blue-300 underline break-all">
              {{ hideAddress(displayMinerInfo.AnchorTxId) }}
            </a>
          </div>
          <div v-if="displayMinerInfo.ServerNode && displayMinerInfo.AscendUtxo"
            class="flex justify-between items-center">
            <span class="text-muted-foreground break-keep">升级UTXO：</span>
            <span class="font-mono text-xs break-all">{{ displayMinerInfo.AscendUtxo }}</span>
          </div>
          <!-- 显示临时质押数据 -->
          <template v-if="tempStakeData && !displayMinerInfo.ServerNode">
            <div class="flex justify-between items-center">
              <span class="text-muted-foreground">交易ID：</span>
              <a :href="generateMempoolUrl({ network: network, path: `tx/${tempStakeData.txId}` })" target="_blank"
                class="font-mono text-xs text-blue-400 hover:text-blue-300 underline break-all">
                {{ hideAddress(tempStakeData.txId) }}
              </a>
            </div>
            <div class="flex justify-between items-center">
              <span class="text-muted-foreground break-keep">预定ID：</span>
              <span class="font-mono text-xs break-all">{{ hideAddress(tempStakeData.resvId) }}</span>
            </div>
            <div class="flex justify-between items-center">
              <span class="text-muted-foreground break-keep">质押资产：</span>
              <span class="font-mono text-xs break-all">{{ tempStakeData.assetName }}</span>
            </div>
            <div class="flex justify-between items-center">
              <span class="text-muted-foreground break-keep">质押数量：</span>
              <span class="font-mono text-xs">{{ tempStakeData.amt }}</span>
            </div>
            <div class="flex justify-between items-center">
              <span class="text-muted-foreground">节点类型：</span>
              <span class="font-mono text-xs">{{ tempStakeData.isCore ? '核心节点' : '挖矿节点' }}</span>
            </div>
          </template>
        </template>
        <template v-else>
          <div class="text-center text-muted-foreground">
            {{ $t('nodeSetting.noNodeInfo', '暂无已绑定节点信息') }}
          </div>
        </template>
      </div>
      <Button variant="secondary" class="h-10 w-full" :disabled="displayMinerInfo.ServerNode || tempStakeData"
        @click="router.push('/wallet/setting/node')">
        {{ $t('nodeSetting.selectNodeType') }}
      </Button>

    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import { useQuery } from '@tanstack/vue-query'
import { useWalletStore } from '@/store'
import { Button } from '@/components/ui/button'
import { satnetApi } from '@/apis'
import { nodeStakeStorage } from '@/lib/nodeStakeStorage'
import { hideAddress, generateMempoolUrl } from '@/utils'
import { Chain, NodeStakeData } from '@/types'

const router = useRouter()
const isExpanded = ref(false)
const tempStakeData = ref<NodeStakeData | null>(null)

const walletStore = useWalletStore()
const { network, publicKey } = storeToRefs(walletStore)

const { data: res, isLoading, isError, error, refetch } = useQuery({
  queryKey: ['minerInfo', publicKey, network],
  queryFn: () => {
    if (!publicKey.value || !network.value) return Promise.resolve({})
    return satnetApi.getMinerInfo({ pubkey: publicKey.value, network: network.value })
  },
  enabled: () => !!publicKey.value && !!network.value,
  initialData: {},
})

const minerInfo = computed(() => res.value?.data || {})
console.log('minerInfo', minerInfo.value);

// 合并显示数据：优先显示服务器数据，如果没有则显示临时数据
const displayMinerInfo = computed(() => {
  if (minerInfo.value.ServerNode) {
    return minerInfo.value
  }
  return {}
})

// 加载临时质押数据
const loadTempStakeData = async () => {
  if (publicKey.value) {
    try {
      const data = await nodeStakeStorage.getNodeStakeData(publicKey.value)
      tempStakeData.value = data
    } catch (error) {
      console.error('Failed to load temp stake data:', error)
    }
  }
}

onMounted(async () => {
  await loadTempStakeData()
})

// 监听服务器数据变化，当有真实节点信息时清除临时数据
watch(minerInfo, async (newMinerInfo) => {
  if (newMinerInfo.ServerNode && tempStakeData.value && publicKey.value) {
    console.log('Server returned real node info, clearing temp data')
    try {
      await nodeStakeStorage.deleteNodeStakeData(publicKey.value)
      tempStakeData.value = null
    } catch (error) {
      console.error('Failed to clear temp stake data:', error)
    }
  }
}, { deep: true })

console.log('minerInfo', minerInfo);
console.log('tempStakeData', tempStakeData);

</script>