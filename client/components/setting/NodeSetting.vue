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
      <div v-if="!isLoading && !isError" class="bg-zinc-800/60 rounded p-3 text-xs text-zinc-200 whitespace-pre-wrap">
        <template v-if="minerInfo && minerInfo.code === 0">
          {{ $t('nodeSetting.bound', '已绑定') }}
        </template>
        <template v-else-if="minerInfo && Object.keys(minerInfo).length">
          {{ JSON.stringify(minerInfo, null, 2) }}
        </template>
        <template v-else>
          {{ $t('nodeSetting.noNodeInfo', '暂无已绑定节点信息') }}
        </template>
      </div>
      <Button as-child variant="secondary" class="h-10 w-full">
        <RouterLink to="/wallet/setting/node" class="w-full">
          {{ $t('nodeSetting.selectNodeType') }}
        </RouterLink>
      </Button>

    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { storeToRefs } from 'pinia'
import { useQuery } from '@tanstack/vue-query'
import { useWalletStore } from '@/store'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { satnetApi } from '@/apis'

const isExpanded = ref(false)
const autoLockTime = ref('5')
const hideBalance = ref(false)

const walletStore = useWalletStore()
const { address, network } = storeToRefs(walletStore)

// useQuery to fetch miner info (node info)
const { data: minerInfo, isLoading, isError, error, refetch } = useQuery({
  queryKey: ['minerInfo', address, network],
  queryFn: () => {
    if (!address.value || !network.value) return Promise.resolve({})
    return satnetApi.getMinerInfo({ address: address.value, network: network.value })
  },
  enabled: () => !!address.value && !!network.value,
  initialData: {},
})

</script>