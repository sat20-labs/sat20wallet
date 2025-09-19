<template>
  <TableRow>
    <TableCell v-if="showCheckbox">
      <Checkbox 
        :model-value="isSelected"
        @update:model-value="() => handleToggleSelect()"
      />
    </TableCell>
    <TableCell class="truncate">
      <a
        :href="explorerUrl"
        target="_blank"
        class="text-blue-400 underline"
      >
        {{ displayUtxo }}
      </a>
    </TableCell>
    <TableCell class="truncate">{{ utxo.reason || '-' }}</TableCell>
    <TableCell class="truncate">{{ formattedLockedTime }}</TableCell>
    <TableCell>
      <Button 
        v-if="!showCheckbox"
        size="sm" 
        variant="default" 
        :loading="isUnlocking" 
        @click="handleUnlock"
      >
        {{ t('utxoManager.unlockBtn') }}
      </Button>
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
  // 如果是SatoshiNet链，跳转到sat20浏览器
  if (props.chain === Chain.SATNET) {
    const baseUrl = props.network === 'mainnet' 
      ? 'https://mainnet.sat20.org/browser/app/#/explorer/utxo'
      : 'https://testnet.sat20.org/browser/app/#/explorer/utxo'
    
    return `${baseUrl}/${props.utxo.utxo}`
  }
  
  // 如果是BTC链，使用mempool
  return generateMempoolUrl({
    network: props.network,
    path: `tx/${props.utxo.txid}`,
    chain: props.chain,
    env: env.value
  })
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
