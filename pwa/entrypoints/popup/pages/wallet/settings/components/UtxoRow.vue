<template>
  <TableRow :data-outpoint="utxo.utxo" class="grid grid-cols-2 rounded-lg border border-border bg-background/50 px-2 md:table-row md:rounded-none md:border-x-0 md:bg-transparent md:p-0">
    <TableCell v-if="showCheckbox && !readOnly">
      <Checkbox :checked="isSelected" :aria-label="displayUtxo" @update:checked="() => handleToggleSelect()" />
    </TableCell>
    <TableCell class="col-span-2 min-w-0 whitespace-normal">
      <a v-if="explorerUrl && !readOnly" :href="explorerUrl" target="_blank" class="text-blue-400 underline">
        {{ displayUtxo }}
      </a>
      <span v-else class="break-all whitespace-normal">{{ readOnly ? utxo.utxo : displayUtxo }}</span>
      <div v-if="readOnly" class="text-xs text-zinc-400 whitespace-normal mt-1">
        {{ utxo.chain === 'btc' ? 'BTC' : 'SatoshiNet' }} · {{ t(utxo.scope === 'current' ? 'utxoManager.currentScope' : 'utxoManager.unknownScope') }}
        <div v-if="utxo.value !== undefined">{{ utxo.value }} sats</div>
        <div v-for="asset in utxo.assets || []" :key="asset.asset" class="break-all">{{ asset.amount }} {{ asset.asset }}</div>
      </div>
    </TableCell>
    <TableCell class="min-w-0 break-words text-sm">
      <span class="block text-xs text-muted-foreground md:hidden">{{ t(showCheckbox ? 'utxoManager.asset' : 'utxoManager.reason') }}</span>
      {{ utxo.reason === 'manual' ? t('settingsUi.manualLock') : utxo.reason || '-' }}
    </TableCell>
    <TableCell class="min-w-0 whitespace-normal text-sm">
      <span class="block text-xs text-muted-foreground md:hidden">{{ t('utxoManager.lockedTime') }}</span>
      {{ formattedLockedTime }}
    </TableCell>
    <TableCell class="col-span-2 min-w-0 whitespace-normal">
      <Button v-if="!showCheckbox && !readOnly" size="sm" variant="default" :loading="isUnlocking" @click="handleUnlock">
        {{ t('utxoManager.unlockBtn') }}
      </Button>
      <div v-else-if="readOnly" class="text-xs text-zinc-400 break-all">
        {{ t('utxoManager.readOnly') }}
        <RouterLink v-for="id in utxo.transferIDs || []" :key="id" to="/wallet?tab=l1" class="block text-blue-400 underline">
          {{ t('utxoManager.relatedTransfer') }}: {{ id }}
        </RouterLink>
      </div>
      <span v-else class="text-sm text-zinc-400">{{ t('utxoManager.selectToUnlock') }}</span>
    </TableCell>
  </TableRow>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { storeToRefs } from 'pinia'
import { useGlobalStore } from '@/store/global'
import { TableRow, TableCell } from '@/components/ui/table'
import { Checkbox } from '@/components/ui/checkbox'
import { Button } from '@/components/ui/button'
import { hideAddress, generateMempoolUrl } from '@/utils'
import { Chain, LockedUtxoInfo } from '@/types'

const { t } = useI18n()
const globalStore = useGlobalStore()
const { env } = storeToRefs(globalStore)

interface Props {
  utxo: LockedUtxoInfo
  showCheckbox: boolean
  readOnly: boolean
  isSelected: boolean
  isUnlocking: boolean
  network: string
  chain: Chain
}

const props = defineProps<Props>()

const emit = defineEmits<{
  toggleSelect: [utxo: string]
  unlock: [utxo: LockedUtxoInfo]
}>()

const displayUtxo = computed(() => hideAddress(props.utxo.utxo))

const explorerUrl = computed(() => {
  if (props.network === 'regtest') return ''
  // 如果是SatoshiNet链，跳转到sat20浏览器
  const baseUrl = props.network === 'testnet'
    ? 'https://testnet.sat20.org/browser/app/#/explorer/utxo'
    : 'https://mainnet.sat20.org/browser/app/#/explorer/utxo'

  return `${baseUrl}/${props.utxo.utxo}`

})

const formattedLockedTime = computed(() =>
  props.utxo.lockedTime
    ? new Date(props.utxo.lockedTime * 1000).toLocaleString()
    : '-'
)

const handleToggleSelect = () => {
  emit('toggleSelect', props.utxo.utxo)
}

const handleUnlock = () => {
  emit('unlock', props.utxo)
}
</script>
