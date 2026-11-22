<template>
  <LayoutSecond :title="t('utxoManager.title')">
    <div class="space-y-4 rounded-xl border border-border bg-card p-3 sm:p-4">
      <Tabs v-model="tab" class="mb-4">
        <TabsList class="grid h-auto w-full grid-cols-2 gap-1 sm:grid-cols-4">
          <TabsTrigger value="btc" class="min-h-11">BTC</TabsTrigger>
          <TabsTrigger value="satsnet" class="min-h-11">SatoshiNet</TabsTrigger>
          <TabsTrigger value="rgb" class="min-h-11">RGB</TabsTrigger>
          <TabsTrigger value="ordinals" class="min-h-11">Ordinals</TabsTrigger>
        </TabsList>
      </Tabs>

      <!-- Manual UTXO Lock Section (for BTC and SatoshiNet) -->
      <ManualLockSection
        v-if="tab === 'btc' || tab === 'satsnet'"
        @lock-utxo="handleLockUtxo"
      />

      <!-- Ordinals UTXO Management Section -->
      <OrdinalsSection
        v-if="tab === 'ordinals'"
        :selected-count="selectedOrdinals.length"
        :unlock-loading="unlockOrdinalsLoading"
        @unlock-selected="unlockSelectedOrdinals"
      />

      <p v-if="tab !== 'ordinals'" class="text-sm text-muted-foreground">{{ t('utxoManager.networkScope', { network: lockNetwork }) }}</p>
      <p v-if="tab === 'rgb'" class="text-sm text-muted-foreground">{{ t('utxoManager.rgbReadOnly') }}</p>
      <p v-if="loadError" role="alert" class="text-sm text-red-400 break-all">{{ loadError }}</p>
      <!-- UTXO Table -->
      <UtxoTable v-if="!loadError"
        :locked-utxos="lockedUtxos"
        :loading="loading"
        :show-checkbox="tab === 'ordinals'"
        :read-only="tab === 'rgb'"
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
import { ref, computed } from 'vue'
import { storeToRefs } from 'pinia'
import { useI18n } from 'vue-i18n'
import { useWalletStore } from '@/store/wallet'
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Chain } from '@/types'
import { useUtxoManager, type UtxoTab } from './composables/useUtxoManager'
import { ManualLockSection, OrdinalsSection, UtxoTable } from './components'

const { t } = useI18n()
const walletStore = useWalletStore()
const { network } = storeToRefs(walletStore)

const tab = ref<UtxoTab>('btc')

// Use the composable for UTXO management logic
const {
  lockedUtxos,
  loadError,
  lockNetwork,
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
} = useUtxoManager(tab)

// Computed properties
const currentChain = computed(() =>
  tab.value === 'btc' ? Chain.BTC : Chain.SATNET
)

// Event handlers
const handleLockUtxo = async (utxoInput: string) => {
  if (tab.value === 'btc' || tab.value === 'satsnet') await lockUtxo(utxoInput, tab.value)
}

const handleUnlockUtxo = async (utxo: any) => {
  const idx = lockedUtxos.value.findIndex(u => u.utxo === utxo.utxo)
  if (tab.value === 'btc' || tab.value === 'satsnet') await unlockUtxo(idx, utxo, tab.value)
}

</script>
