<template>
  <div>
    <h3 class="text-base font-bold text-zinc-200 mb-2">{{ t('utxoManager.lockedList') }}</h3>

    <!-- Loading State -->
    <div v-if="loading" class="text-center py-8 text-muted-foreground">
      {{ t('utxoManager.loading') }}
    </div>

    <!-- Empty State -->
    <div v-else-if="lockedUtxos.length === 0" class="rounded-lg border border-dashed border-border py-8 text-center text-sm text-muted-foreground">
      {{ t('utxoManager.empty') }}
    </div>

    <!-- Table -->
    <Table v-else>
      <TableHeader :class="showCheckbox ? '' : 'hidden md:table-header-group'">
        <TableRow>
          <TableHead v-if="showCheckbox">
            <Checkbox
              :checked="isAllSelected"
              :aria-label="t('utxoManager.lockedList')"
              @update:checked="() => handleToggleSelectAll()"
            />
          </TableHead>
          <TableHead class="hidden md:table-cell">UTXO</TableHead>
          <TableHead class="hidden md:table-cell">{{ showCheckbox ? t('utxoManager.asset') : t('utxoManager.reason') }}</TableHead>
          <TableHead class="hidden md:table-cell">{{ t('utxoManager.lockedTime') }}</TableHead>
          <TableHead class="hidden md:table-cell">{{ t('utxoManager.action') }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody class="block space-y-2 md:table-row-group md:space-y-0">
        <UtxoRow
          v-for="(utxo, idx) in lockedUtxos"
          :key="`${utxo.chain || chain}:${utxo.utxo}`"
          :utxo="utxo"
          :show-checkbox="showCheckbox"
          :read-only="readOnly"
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
  readOnly: boolean
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
