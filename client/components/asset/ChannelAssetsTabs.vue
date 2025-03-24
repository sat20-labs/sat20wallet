<template>
  <!-- Asset Type Tabs -->
  <div class="border-b border-border/50 mb-4">
    <nav class="flex -mb-px gap-4">
      <button
        v-for="(type, index) in assetTypes"
        :key="index"
        @click="selectedAssetType = type"
        class="pb-2 px-1 font-mono font-semibold text-sm relative"
        :class="{
          'text-foreground/90': selectedAssetType === type,
          'text-muted-foreground': selectedAssetType !== type
        }"
      >
        {{ type }}
        <div
          class="absolute bottom-0 left-0 right-0 h-0.5 transition-all"
          :class="{
            'bg-gradient-to-r from-primary to-primary/50 scale-x-100': selectedAssetType === type,
            'scale-x-0': selectedAssetType !== type
          }"
        />
      </button>
    </nav>
  </div>

  <!-- Asset List -->
  <div class="space-y-2">
    <template v-if="selectedAssetType === 'BTC' && plainList.length">
      <div
        v-for="asset in plainList"
        :key="asset.id"
        class="flex items-center justify-between p-3 rounded-lg bg-muted/40 hover:bg-muted/60 transition-colors"
      >
        <div>
          <div class="font-medium">BTC</div>
          <div class="text-sm text-muted-foreground">
            {{ formatAmount(asset) }} sats
          </div>
        </div>
        <div class="flex gap-2">
          <Button size="sm" variant="outline" asChild>
            <RouterLink :to="`/wallet/asset?type=unlock&p=btc&t=utxo&a=${asset.id}&chanId=${channel?.chanid}`">
              <Icon icon="lucide:unlock" class="w-4 h-4 mr-1" />
              Unlock
            </RouterLink>
          </Button>
          <Button size="sm" variant="outline" asChild>
            <RouterLink :to="`/wallet/asset?type=splicing_out&p=btc&t=utxo&a=${asset.id}&chanId=${channel?.chanid}`">
              <Icon icon="lucide:corner-up-right" class="w-4 h-4 mr-1" />
              Splicing Out
            </RouterLink>
          </Button>
        </div>
      </div>
    </template>

    <template v-if="selectedAssetType === 'SAT20' && sat20List.length">
      <div
        v-for="asset in sat20List"
        :key="asset.id"
        class="flex items-center justify-between p-3 rounded-lg bg-muted/40 hover:bg-muted/60 transition-colors"
      >
        <div>
          <div class="font-medium">{{ asset.label.toUpperCase() }}</div>
          <div class="text-sm text-muted-foreground">
            {{ formatAmount(asset) }} {{ asset.ticker }}
          </div>
        </div>
        <div class="flex gap-2">
          <Button size="sm" variant="outline" asChild>
            <RouterLink :to="`/wallet/asset?type=unlock&p=ordx&t=${asset.type}&a=${asset.id}&chanId=${channel?.chanid}`">
              <Icon icon="lucide:unlock" class="w-4 h-4 mr-1" />
              Unlock
            </RouterLink>
          </Button>
          <Button size="sm" variant="outline" asChild>
            <RouterLink :to="`/wallet/asset?type=splicing_out&p=ordx&t=${asset.type}&a=${asset.id}&chanId=${channel?.chanid}`">
              <Icon icon="lucide:corner-up-right" class="w-4 h-4 mr-1" />
              Splicing Out
            </RouterLink>
          </Button>
        </div>
      </div>
    </template>

    <template v-if="selectedAssetType === 'BRC20' && brc20List.length">
      <div
        v-for="asset in brc20List"
        :key="asset.id"
        class="flex items-center justify-between p-3 rounded-lg bg-muted/40 hover:bg-muted/60 transition-colors"
      >
        <div>
          <div class="font-medium">{{ asset.label.toUpperCase() }}</div>
          <div class="text-sm text-muted-foreground">
            {{ formatAmount(asset) }} {{ asset.ticker }}
          </div>
        </div>
        <div class="flex gap-2">
          <Button size="sm" variant="outline" asChild>
            <RouterLink :to="`/wallet/asset?type=unlock&p=brc20&t=${asset.type}&a=${asset.id}&chanId=${channel?.chanid}`">
              <Icon icon="lucide:unlock" class="w-4 h-4 mr-1" />
              Unlock
            </RouterLink>
          </Button>
          <Button size="sm" variant="outline" asChild>
            <RouterLink :to="`/wallet/asset?type=splicing_out&p=brc20&t=${asset.type}&a=${asset.id}&chanId=${channel?.chanid}`">
              <Icon icon="lucide:corner-up-right" class="w-4 h-4 mr-1" />
              Splicing Out
            </RouterLink>
          </Button>
        </div>
      </div>
    </template>

    <template v-if="selectedAssetType === 'Runes' && runesList.length">
      <div
        v-for="asset in runesList"
        :key="asset.id"
        class="flex items-center justify-between p-3 rounded-lg bg-muted/40 hover:bg-muted/60 transition-colors"
      >
        <div>
          <div class="font-medium">{{ asset.label.toUpperCase() }}</div>
          <div class="text-sm text-muted-foreground">
            {{ formatAmount(asset) }} {{ asset.ticker }}
          </div>
        </div>
        <div class="flex gap-2">
          <Button size="sm" variant="outline" asChild>
            <RouterLink :to="`/wallet/asset?type=unlock&p=runes&t=${asset.type}&a=${asset.id}&chanId=${channel?.chanid}`">
              <Icon icon="lucide:unlock" class="w-4 h-4 mr-1" />
              Unlock
            </RouterLink>
          </Button>
          <Button size="sm" variant="outline" asChild>
            <RouterLink :to="`/wallet/asset?type=splicing_out&p=runes&t=${asset.type}&a=${asset.id}&chanId=${channel?.chanid}`">
              <Icon icon="lucide:corner-up-right" class="w-4 h-4 mr-1" />
              Splicing Out
            </RouterLink>
          </Button>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { Button } from '@/components/ui/button'
import { Icon } from '@iconify/vue'
import { RouterLink } from 'vue-router'
import { useChannelStore } from '~/store'

const channelStore = useChannelStore()
const { channel, plainList, sat20List, brc20List, runesList } = storeToRefs(channelStore)

// 资产类型
const assetTypes = ['BTC', 'SAT20', 'BRC20', 'Runes']
const selectedAssetType = ref('BTC')

// 格式化金额显示
const formatAmount = (asset: any) => {
  return asset.amount || 0
}

// 监听资产类型变化
watch(selectedAssetType, (newType) => {
  console.log('Selected asset type changed:', newType)
})
</script>

<style scoped>
.router-link-active {
  text-decoration: none;
}
</style>
