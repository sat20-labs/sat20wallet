<template>
  <LayoutSecond :title="t('utxoManager.title')">
    <div class="w-full max-w-2xl mx-auto bg-zinc-700/40 rounded-lg p-4">
      <Tabs v-model="tab" class="mb-4">
        <TabsList class="border border-zinc-700 rounded-lg">
          <TabsTrigger value="btc" class="bg-zinc-800 data-[state=active]:bg-purple-700 data-[state=active]:text-zinc-200">BTC</TabsTrigger>
          <TabsTrigger value="satsnet" class="bg-zinc-800 data-[state=active]:bg-purple-700 data-[state=active]:text-zinc-200">SatoshiNet</TabsTrigger>
          <TabsTrigger value="ordinals" class="bg-zinc-800 data-[state=active]:bg-purple-700 data-[state=active]:text-zinc-200">Ordinals</TabsTrigger>
        </TabsList>
      </Tabs>
      <hr class="my-4 border-zinc-900" />
      
      <!-- Manual UTXO Lock Section (for BTC and SatoshiNet) -->
      <ManualLockSection 
        v-if="tab !== 'ordinals'" 
        @lock-utxo="handleLockUtxo"
      />
      
      <!-- Ordinals UTXO Management Section -->
      <OrdinalsSection 
        v-if="tab === 'ordinals'"
        :selected-count="selectedOrdinals.length"
        :unlock-loading="unlockOrdinalsLoading"
        @unlock-selected="unlockSelectedOrdinals"
      />
      
      <!-- UTXO Table -->
      <UtxoTable
        :locked-utxos="lockedUtxos"
        :loading="loading"
        :show-checkbox="tab === 'ordinals'"
        :selected-utxos="selectedOrdinals"
        :unlocking-idx="unlockingIdx"
        :network="network"
        :chain="currentChain"
        @toggle-select-all="toggleSelectAll"
        @toggle-select="toggleSelectUtxo"
        @unlock="handleUnlockUtxo"
      />
    </div>
  </LayoutSecond>
</template>

<script setup lang="ts">
import { ref, watch, onMounted, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useWalletStore } from '@/store/wallet'
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Chain } from '@/types'
import { useUtxoManager } from './composables/useUtxoManager'
import { ManualLockSection, OrdinalsSection, UtxoTable } from './components'

const { t } = useI18n()
const walletStore = useWalletStore()
const { network } = walletStore

const tab = ref<'btc' | 'satsnet' | 'ordinals'>('btc')

// Use the composable for UTXO management logic
const {
  lockedUtxos,
  loading,
  lockLoading,
  unlockingIdx,
  unlockOrdinalsLoading,
  selectedOrdinals,
  fetchLockedUtxos,
  lockUtxo,
  unlockUtxo,
  toggleSelectUtxo,
  toggleSelectAll,
  unlockSelectedOrdinals
} = useUtxoManager()

// Computed properties
const currentChain = computed(() => 
  tab.value === 'btc' ? Chain.BTC : Chain.SATNET
)

// Event handlers
const handleLockUtxo = async (utxoInput: string) => {
  await lockUtxo(utxoInput, tab.value as 'btc' | 'satsnet')
}

const handleUnlockUtxo = async (utxo: any) => {
  const idx = lockedUtxos.value.findIndex(u => u.utxo === utxo.utxo)
  await unlockUtxo(idx, utxo, tab.value as 'btc' | 'satsnet')
}

// Watchers and lifecycle
watch(tab, () => fetchLockedUtxos(tab.value), { immediate: true })
onMounted(() => fetchLockedUtxos(tab.value))
</script>
