<template>
  <div>
    <Button size="sm" variant="outline" :disabled="switching" @click="isOpen = true" class="min-h-10 gap-1 px-2" :aria-label="$t('networkSelect.selectNetwork')">
      <Icon v-if="switching" icon="lucide:loader-2" class="size-4 animate-spin" aria-hidden="true" />
      <span v-if="showNetwork" class="text-xs">{{ $t(`networkSelect.${showNetwork.name}`) }}</span>
      <ChevronDown class="h-4 w-4" />
    </Button>

    <Dialog v-model:open="isOpen">
      <DialogContent class="w-[calc(100%_-_2rem)] max-w-sm rounded-xl">
        <DialogHeader>
          <DialogTitle class="text-gray-300 text-xl font-semibold">{{ $t('networkSelect.selectNetwork') }}</DialogTitle>
        </DialogHeader>
        <div class="space-y-2 py-4">
          <Button
            v-for="n in networks"
            :key="n.value"
            @click="selectNetwork(n)"
            :disabled="switching"
            :variant="n.value === network ? 'secondary' : 'outline'"
            class="w-full justify-start h-12"
          >
            <div class="flex items-center gap-3">
              <Icon :icon="n.icon" :class="n.iconColor" />
              <span>{{ $t(`networkSelect.${n.name}`) }}</span>
            </div>
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { ChevronDown } from 'lucide-vue-next'
import { Icon } from '@iconify/vue'
import walletManager from '@/utils/sat20'
import { Network } from '@/types'
import { useWalletStore } from '@/store'
import { ref, computed, nextTick } from 'vue'
import { storeToRefs } from 'pinia'
import { restartApp } from '@/utils/app-restart'
import { useToast } from '@/components/ui/toast-new'
import { isRegtestBuild } from '@/utils/networkMode'

const { t } = useI18n()

interface NetworkItem {
  name: string
  icon: string
  iconColor: string
  value: Network
}
const walletStore = useWalletStore()
const { network } = storeToRefs(walletStore)
const publicNetworks: NetworkItem[] = [
  {
    name: 'Mainnet',
    icon: 'cryptocurrency:btc',
    iconColor: 'text-orange-500',
    value: Network.MAINNET,
  },
  {
    name: 'Testnet',
    icon: 'cryptocurrency:btc',
    iconColor: 'text-green-500',
    value: Network.TESTNET,
  },
]
const networks: NetworkItem[] = isRegtestBuild ? [
  {
    name: 'Regtest',
    icon: 'cryptocurrency:btc',
    iconColor: 'text-amber-400',
    value: Network.REGTEST,
  },
] : publicNetworks

const isOpen = ref(false) // 默认不打开对话框
const switching = ref(false)
const { toast } = useToast()

const selectNetwork = async (item: NetworkItem) => {
  if (switching.value) return
  isOpen.value = false
  if (item.value === network.value) return
  await nextTick()
  switching.value = true
  try {
    const changed = await walletStore.setNetwork(item.value)
    if (changed) restartApp()
  } catch (error) {
    console.error('Network switch failed:', error)
    toast({
      title: t('common.error'),
      description: error instanceof Error ? error.message : t('pwaUi.network.switchFailed'),
      variant: 'destructive',
    })
  } finally {
    switching.value = false
  }
}

const showNetwork = computed(() =>
  networks.find((n) => n.value === network.value)
)
</script>
