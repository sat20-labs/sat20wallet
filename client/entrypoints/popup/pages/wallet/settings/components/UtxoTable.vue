<template>
  <div>
    <h3 class="text-base font-bold text-zinc-200 mb-2">{{ t('utxoManager.lockedList') }}</h3>
    
    <!-- Loading State -->
    <div v-if="loading" class="text-center py-8 text-muted-foreground">
      {{ t('utxoManager.loading') }}
    </div>
    
    <!-- Empty State -->
    <div v-else-if="lockedUtxos.length === 0" class="text-center py-8 text-muted-foreground">
      {{ t('utxoManager.empty') }}
    </div>
    
    <!-- Table -->
    <Table v-else>
      <TableHeader>
        <TableRow>
          <TableHead v-if="showCheckbox">
            <Checkbox 
              :model-value="isAllSelected"
              @update:model-value="() => handleToggleSelectAll()"
            />
          </TableHead>
          <TableHead>UTXO</TableHead>
          <TableHead>{{ showCheckbox ? t('utxoManager.asset') : t('utxoManager.reason') }}</TableHead>
          <TableHead>{{ t('utxoManager.lockedTime') }}</TableHead>
          <TableHead>{{ t('utxoManager.action') }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <UtxoRow
          v-for="(utxo, idx) in lockedUtxos"
          :key="utxo.utxo"
          :utxo="utxo"
          :show-checkbox="showCheckbox"
          :is-selected="selectedUtxos.includes(utxo.utxo)"
          :is-unlocking="unlockingIdx === idx"
          :network="network"
          :chain="chain"
          @toggle-select="handleToggleSelect"
          @unlock="handleUnlock"
        />
      </TableBody>
    </Table>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Table, TableHeader, TableRow, TableHead, TableBody } from '@/components/ui/table'
import { Checkbox } from '@/components/ui/checkbox'
import UtxoRow from './UtxoRow.vue'
import { Chain, LockedUtxoInfo } from '@/types'

const { t } = useI18n()

interface Props {
  lockedUtxos: LockedUtxoInfo[]
  loading: boolean
  showCheckbox: boolean
  selectedUtxos: string[]
  unlockingIdx: number
  network: string
  chain: Chain
}

const props = defineProps<Props>()

const emit = defineEmits<{
  toggleSelectAll: []
  toggleSelect: [utxo: string]
  unlock: [utxo: LockedUtxoInfo]
}>()

const isAllSelected = computed(() => 
  props.lockedUtxos.length > 0 && 
  props.selectedUtxos.length === props.lockedUtxos.length
)

const handleToggleSelectAll = () => {
  emit('toggleSelectAll')
}

const handleToggleSelect = (utxo: string) => {
  emit('toggleSelect', utxo)
}

const handleUnlock = (utxo: LockedUtxoInfo) => {
  emit('unlock', utxo)
}
</script>
