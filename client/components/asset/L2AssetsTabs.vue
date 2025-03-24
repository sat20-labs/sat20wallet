<template>
  <div class="space-y-4">
    <!-- Asset Type Tabs -->
    <div class="border-b border-border/50 mb-4">
      <nav class="flex -mb-px gap-4">
        <button
          v-for="(type, index) in assetTypes"
          :key="index"
          @click="selectedType = type"
          class="pb-2 px-1 font-mono font-semibold text-sm relative"
          :class="{
            'text-foreground/90': selectedType === type,
            'text-muted-foreground': selectedType !== type
          }"
        >
          {{ type }}
          <div
            class="absolute bottom-0 left-0 right-0 h-0.5 transition-all"
            :class="{
              'bg-gradient-to-r from-primary to-primary/50 scale-x-100': selectedType === type,
              'scale-x-0': selectedType !== type
            }"
          />
        </button>
      </nav>
    </div>

    <!-- Asset Lists -->
    <div class="space-y-2">
      <template v-if="selectedType === 'BTC'">
        <div
          v-for="asset in plainList"
          :key="asset.id"
          class="flex items-center justify-between p-3 rounded-lg bg-muted/40 hover:bg-muted/60 transition-colors"
        >
          <div>
            <div class="font-medium">BTC</div>
            <div class="text-sm text-muted-foreground">
              {{ asset.amount }} sats
            </div>
          </div>
          <div class="flex gap-2">
            <Button size="sm" variant="outline" asChild>
              <RouterLink :to="`/wallet/asset?type=l2_send&p=btc&t=${asset.type}&a=${asset.id}`">
                <Icon icon="lucide:arrow-right" class="w-4 h-4 mr-1" />
                Send
              </RouterLink>
            </Button>
            <Button v-if="channel && channel.status === 16" size="sm" variant="outline" asChild>
              <RouterLink :to="`/wallet/asset?type=lock&p=btc&t=${asset.type}&a=${asset.id}`">
                <Icon icon="lucide:lock" class="w-4 h-4 mr-1" />
                Lock
              </RouterLink>
            </Button>
          </div>
        </div>
      </template>

      <template v-if="selectedType === 'SAT20'">
        <div
          v-for="asset in sat20List"
          :key="asset.id"
          class="flex items-center justify-between p-3 rounded-lg bg-muted/40 hover:bg-muted/60 transition-colors"
        >
          <div>
            <div class="font-medium">{{ asset.label.toUpperCase() }}</div>
            <div class="text-sm text-muted-foreground">
              {{ asset.amount }} {{ asset.ticker }}
            </div>
          </div>
          <div class="flex gap-2">
            <Button size="sm" variant="outline" asChild>
              <RouterLink :to="`/wallet/asset?type=l2_send&p=ordx&t=${asset.type}&a=${asset.id}&l=l2`">
                <Icon icon="lucide:arrow-right" class="w-4 h-4 mr-1" />
                Send
              </RouterLink>
            </Button>
            <Button v-if="channel && channel.status === 16" size="sm" variant="outline" asChild>
              <RouterLink :to="`/wallet/asset?type=lock&p=ordx&t=${asset.type}&a=${asset.id}`">
                <Icon icon="lucide:lock" class="w-4 h-4 mr-1" />
                Lock
              </RouterLink>
            </Button>
          </div>
        </div>
      </template>

      <template v-if="selectedType === 'BRC20'">
        <div
          v-for="asset in brc20List"
          :key="asset.id"
          class="flex items-center justify-between p-3 rounded-lg bg-muted/40 hover:bg-muted/60 transition-colors"
        >
          <div>
            <div class="font-medium">{{ asset.label.toUpperCase() }}</div>
            <div class="text-sm text-muted-foreground">
              {{ asset.amount }} {{ asset.ticker }}
            </div>
          </div>
          <div class="flex gap-2">
            <Button size="sm" variant="outline" asChild>
              <RouterLink :to="`/wallet/asset?type=l2_send&p=brc20&t=${asset.type}&a=${asset.id}&l=l2`">
                <Icon icon="lucide:arrow-right" class="w-4 h-4 mr-1" />
                Send
              </RouterLink>
            </Button>
            <Button v-if="channel && channel.status === 16" size="sm" variant="outline" asChild>
              <RouterLink :to="`/wallet/asset?type=lock&p=brc20&t=${asset.type}&a=${asset.id}`">
                <Icon icon="lucide:lock" class="w-4 h-4 mr-1" />
                Lock
              </RouterLink>
            </Button>
          </div>
        </div>
      </template>

      <template v-if="selectedType === 'Runes'">
        <div
          v-for="asset in runesList"
          :key="asset.id"
          class="flex items-center justify-between p-3 rounded-lg bg-muted/40 hover:bg-muted/60 transition-colors"
        >
          <div>
            <div class="font-medium">{{ asset.label.toUpperCase() }}</div>
            <div class="text-sm text-muted-foreground">
              {{ asset.amount }} {{ asset.ticker }}
            </div>
          </div>
          <div class="flex gap-2">
            <Button size="sm" variant="outline" asChild>
              <RouterLink :to="`/wallet/asset?type=l2_send&p=runes&t=${asset.type}&a=${asset.id}&l=l2`">
                <Icon icon="lucide:arrow-right" class="w-4 h-4 mr-1" />
                Send
              </RouterLink>
            </Button>
            <Button v-if="channel && channel.status === 16" size="sm" variant="outline" asChild>
              <RouterLink :to="`/wallet/asset?type=lock&p=runes&t=${asset.type}&a=${asset.id}`">
                <Icon icon="lucide:lock" class="w-4 h-4 mr-1" />
                Lock
              </RouterLink>
            </Button>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { Button } from '@/components/ui/button'
import { Icon } from '@iconify/vue'
import { RouterLink } from 'vue-router'
import { useL2Store, useChannelStore } from '@/store'

const props = defineProps<{
  modelValue?: string
}>()

const emit = defineEmits(['update:modelValue'])

const l2Store = useL2Store()
const channelStore = useChannelStore()
const { channel } = storeToRefs(channelStore)
const { runesList, plainList, sat20List, brc20List } = storeToRefs(l2Store)

const assetTypes = ['BTC', 'SAT20', 'BRC20', 'Runes']
const selectedType = ref(props.modelValue || assetTypes[0])

watch(selectedType, (newType) => {
  emit('update:modelValue', newType)
})
</script>

<style scoped>
.router-link-active {
  text-decoration: none;
}
</style>