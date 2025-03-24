<template>
  <div class="space-y-4">
    <!-- BTC Balance Section -->
    <div>
      <div class="text-lg font-bold mb-2">BTC Balance</div>
      <div class="space-y-2">
        <div
          v-for="asset in plainList"
          :key="asset.id"
          class="flex items-center justify-between p-3 rounded-lg bg-muted/40 hover:bg-muted/60 transition-colors"
        >
          <div>
            <div class="font-medium">BTC</div>
            <div class="text-sm text-muted-foreground">
              {{ formatAmount(asset, 'BTC') }}
            </div>
          </div>
          <div class="flex gap-2">
            <Button size="sm" variant="outline" asChild>
              <RouterLink :to="`/wallet/asset?type=unlock&p=btc&t=utxo&a=${asset.id}`">
                <Icon icon="lucide:unlock" class="w-4 h-4 mr-1" />
                Unlock
              </RouterLink>
            </Button>
            <Button size="sm" variant="outline" asChild>
              <RouterLink :to="`/wallet/asset?type=splicing_out&p=btc&t=utxo&a=${asset.id}`">
                <Icon icon="lucide:corner-up-right" class="w-4 h-4 mr-1" />
                Splicing Out
              </RouterLink>
            </Button>
          </div>
        </div>
      </div>
    </div>

    <!-- SAT20 Section -->
    <div>
      <div class="text-lg font-bold mb-2">SAT20</div>
      <div class="space-y-2">
        <div
          v-for="asset in sat20List"
          :key="asset.id"
          class="flex items-center justify-between p-3 rounded-lg bg-muted/40 hover:bg-muted/60 transition-colors"
        >
          <div>
            <div class="font-medium">{{ asset.label.toUpperCase() }}</div>
            <div class="text-sm text-muted-foreground">
              {{ formatAmount(asset, 'SAT20') }}
            </div>
          </div>
          <div class="flex gap-2">
            <Button size="sm" variant="outline" asChild>
              <RouterLink :to="`/wallet/asset?type=unlock&p=ordx&t=${asset.type}&a=${asset.id}`">
                <Icon icon="lucide:unlock" class="w-4 h-4 mr-1" />
                Unlock
              </RouterLink>
            </Button>
            <Button size="sm" variant="outline" asChild>
              <RouterLink :to="`/wallet/asset?type=splicing_out&p=ordx&t=${asset.type}&a=${asset.id}`">
                <Icon icon="lucide:corner-up-right" class="w-4 h-4 mr-1" />
                Splicing Out
              </RouterLink>
            </Button>
          </div>
        </div>
      </div>
    </div>

    <!-- BRC20 Section -->
    <div>
      <div class="text-lg font-bold mb-2">BRC20</div>
      <div class="space-y-2">
        <div
          v-for="asset in brc20List"
          :key="asset.id"
          class="flex items-center justify-between p-3 rounded-lg bg-muted/40 hover:bg-muted/60 transition-colors"
        >
          <div>
            <div class="font-medium">{{ asset.label.toUpperCase() }}</div>
            <div class="text-sm text-muted-foreground">
              {{ formatAmount(asset, 'BRC20') }}
            </div>
          </div>
          <div class="flex gap-2">
            <Button size="sm" variant="outline" asChild>
              <RouterLink :to="`/wallet/asset?type=unlock&p=brc20&t=${asset.type}&a=${asset.id}`">
                <Icon icon="lucide:unlock" class="w-4 h-4 mr-1" />
                Unlock
              </RouterLink>
            </Button>
            <Button size="sm" variant="outline" asChild>
              <RouterLink :to="`/wallet/asset?type=splicing_out&p=brc20&t=${asset.type}&a=${asset.id}`">
                <Icon icon="lucide:corner-up-right" class="w-4 h-4 mr-1" />
                Splicing Out
              </RouterLink>
            </Button>
          </div>
        </div>
      </div>
    </div>

    <!-- Runes Section -->
    <div>
      <div class="text-lg font-bold mb-2">Runes</div>
      <div class="space-y-2">
        <div
          v-for="asset in runesList"
          :key="asset.id"
          class="flex items-center justify-between p-3 rounded-lg bg-muted/40 hover:bg-muted/60 transition-colors"
        >
          <div>
            <div class="font-medium">{{ asset.label.toUpperCase() }}</div>
            <div class="text-sm text-muted-foreground">
              {{ formatAmount(asset, 'RUNES') }}
            </div>
          </div>
          <div class="flex gap-2">
            <Button size="sm" variant="outline" asChild>
              <RouterLink :to="`/wallet/asset?type=unlock&p=runes&t=${asset.type}&a=${asset.id}`">
                <Icon icon="lucide:unlock" class="w-4 h-4 mr-1" />
                Unlock
              </RouterLink>
            </Button>
            <Button size="sm" variant="outline" asChild>
              <RouterLink :to="`/wallet/asset?type=splicing_out&p=runes&t=${asset.type}&a=${asset.id}`">
                <Icon icon="lucide:corner-up-right" class="w-4 h-4 mr-1" />
                Splicing Out
              </RouterLink>
            </Button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { storeToRefs } from 'pinia'
import { Button } from '@/components/ui/button'
import { Icon } from '@iconify/vue'
import { useChannelStore } from '~/store'

const channelStore = useChannelStore()
const { plainList, sat20List, runesList, brc20List } = storeToRefs(channelStore)

// 格式化金额显示
const formatAmount = (asset: any, type: string) => {
  if (type === 'BTC') {
    return `${asset.amount} sats`
  }
  return `${asset.amount} ${asset.ticker || asset.label}`
}
</script>

<style scoped>
.router-link-active {
  text-decoration: none;
}
</style>
