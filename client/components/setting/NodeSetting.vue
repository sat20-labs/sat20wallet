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
        <template v-if="minerInfo.ServerNode">
          <div class="flex justify-between items-center">
            <span class="text-muted-foreground break-keep">连接的节点：</span>
            <span class="font-mono text-xs break-all">{{ minerInfo.ServerNode }}</span>
          </div>
          <div class="flex justify-between items-center">
            <span class="text-muted-foreground break-keep">本地节点：</span>
            <span class="font-mono text-xs break-all">{{ publicKey }}</span>
          </div>
          <div class="flex justify-between items-center">
            <span class="text-muted-foreground break-keep">通道地址：</span>
            <a :href="generateMempoolUrl({
              network: network,
              path: `address/${minerInfo.ChannelAddr}`,
            })" target="_blank" class="font-mono text-xs text-blue-400 hover:text-blue-300 underline break-all">
              {{ minerInfo.ChannelAddr }}
            </a>
          </div>
          <div class="flex justify-between items-center">
            <span class="text-muted-foreground">质押资产：</span>
            <span class="font-mono text-xs">{{ minerInfo.AssetName }}</span>
          </div>
          <div class="flex justify-between items-center">
            <span class="text-muted-foreground">资产数量：</span>
            <span class="font-mono text-xs">{{ minerInfo.AssetAmt }}</span>
          </div>
        </template>
        <template v-else>
          <div class="text-center text-muted-foreground">
            {{ $t('nodeSetting.noNodeInfo', '暂无已绑定节点信息') }}
          </div>
        </template>
      </div>
      <Button 
        variant="secondary" 
        class="h-10 w-full"
        :disabled="!minerInfo.ServerNode"
        @click="router.push('/wallet/setting/node')"
      >
        {{ $t('nodeSetting.selectNodeType') }}
      </Button>

    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import { useQuery } from '@tanstack/vue-query'
import { useWalletStore } from '@/store'
import { Button } from '@/components/ui/button'
import { satnetApi } from '@/apis'

const router = useRouter()
const isExpanded = ref(false)

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
</script>