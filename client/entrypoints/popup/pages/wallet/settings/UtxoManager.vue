<template>
  <LayoutSecond :title="$t('utxoManager.title')">
    <div class="w-full max-w-2xl mx-auto bg-zinc-700/40 rounded-lg p-4">
      <Tabs v-model="tab" class="mb-4">
        <TabsList class="border border-zinc-700 rounded-lg">
          <TabsTrigger value="btc" class="bg-zinc-800 data-[state=active]:bg-purple-700 data-[state=active]:text-zinc-200">BTC</TabsTrigger>
          <TabsTrigger value="satsnet" class="bg-zinc-800 data-[state=active]:bg-purple-700 data-[state=active]:text-zinc-200">SatoshiNet</TabsTrigger>
        </TabsList>
      </Tabs>
      <hr class="my-4 border-zinc-900" />
      <div class="mb-4 flex gap-2">
        <Input v-model="utxoInput" :placeholder="$t('utxoManager.inputPlaceholder')" class="flex-1" />
        <Button :loading="lockLoading" @click="lockUtxo" variant="default">{{ $t('utxoManager.lockBtn') }}</Button>
      </div>
      <div>
        <h3 class="text-base font-bold text-zinc-200 mb-2">{{ $t('utxoManager.lockedList') }}</h3>
        <div v-if="loading" class="text-center py-8 text-muted-foreground">{{ $t('utxoManager.loading') }}</div>
        <div v-else-if="lockedUtxos.length === 0" class="text-center py-8 text-muted-foreground">{{ $t('utxoManager.empty') }}</div>
        <Table v-else>
          <TableHeader>
            <TableRow>
              <TableHead>UTXO</TableHead>
              <TableHead>{{ $t('utxoManager.reason') }}</TableHead>
              <TableHead>{{ $t('utxoManager.lockedTime') }}</TableHead>
              <TableHead>{{ $t('utxoManager.action') }}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="(utxo, idx) in lockedUtxos" :key="idx">
              <TableCell class="truncate">
                <a
                  :href="generateMempoolUrl({
                    network: 'testnet',
                    path: `tx/${utxo.txid}`,
                    chain: tab === 'btc' ? Chain.BTC : Chain.SATNET,
                    env: env
                  })"
                  target="_blank"
                  class="text-blue-400 underline"
                >
                  {{ hideAddress(utxo.utxo) }}
                </a>
              </TableCell>
              <TableCell class="truncate">{{ utxo.reason || '-' }}</TableCell>
              <TableCell class="truncate">{{ utxo.lockedTime ? new Date(utxo.lockedTime * 1000).toLocaleString() : '-' }}</TableCell>
              <TableCell>
                <Button size="sm" variant="default" :loading="unlockingIdx === idx" @click="unlockUtxo(idx, utxo)">
                  {{ $t('utxoManager.unlockBtn') }}
                </Button>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>
    </div>
  </LayoutSecond>
</template>

<script setup lang="ts">
import { ref, watch, onMounted, computed } from 'vue'
import { useWalletStore } from '@/store/wallet'
import satsnetStp from '@/utils/stp'
import { useToast } from '@/components/ui/toast/use-toast'
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { Table, TableHeader, TableRow, TableHead, TableBody, TableCell } from '@/components/ui/table'
import { hideAddress, generateMempoolUrl } from '@/utils'
import { Chain } from '@/types'
import { storeToRefs } from 'pinia'
import { useGlobalStore } from '@/store/global'

const walletStore = useWalletStore()
const globalStore = useGlobalStore()
const { address } = walletStore
const { toast } = useToast()

const tab = ref<'btc' | 'satsnet'>('btc')
const lockedUtxos = ref<any[]>([])
const loading = ref(false)
const utxoInput = ref('')
const lockLoading = ref(false)
const unlockingIdx = ref(-1)
const { env } = storeToRefs(globalStore)
const addressStr = computed(() => address || '')

const fetchLockedUtxos = async () => {
  loading.value = true
  let res
  if (tab.value === 'btc') {
    [, res] = await satsnetStp.getAllLockedUtxo(addressStr.value)
  } else {
    [, res] = await satsnetStp.getAllLockedUtxo_SatsNet(addressStr.value)
  }
  lockedUtxos.value = Object.entries(res || {}).map(([utxo, infoStr]) => {
    let info
    try {
      info = JSON.parse(infoStr)
    } catch (e) {
      info = {}
    }
    return {
      utxo,
      txid: utxo.split(':')[0],
      vout: utxo.split(':')[1],
      ...info
    }
  })
  loading.value = false
}
const lockUtxo = async () => {
  if (!utxoInput.value) return toast({ title: 'Error', description: '请输入UTXO', variant: 'destructive' })
  lockLoading.value = true
  let err
  if (tab.value === 'btc') {
    [err] = await satsnetStp.lockUtxo(addressStr.value, utxoInput.value)
  } else {
    [err] = await satsnetStp.lockUtxo_SatsNet(addressStr.value, utxoInput.value)
  }
  lockLoading.value = false
  if (err) {
    toast({ title: 'Error', description: '锁定失败', variant: 'destructive' })
  } else {
    toast({ title: 'Success', description: '锁定成功' })
    utxoInput.value = ''
    fetchLockedUtxos()
  }
}

const unlockUtxo = async (idx: number, utxo: any) => {
  unlockingIdx.value = idx
  let err
  if (tab.value === 'btc') {
    [err] = await satsnetStp.unlockUtxo(addressStr.value, utxo.utxo)
  } else {
    [err] = await satsnetStp.unlockUtxo_SatsNet(addressStr.value, utxo.utxo)
  }
  unlockingIdx.value = -1
  if (err) {
    toast({ title: 'Error', description: '解锁失败', variant: 'destructive' })
  } else {
    toast({ title: 'Success', description: '解锁成功' })
    fetchLockedUtxos()
  }
}

watch(tab, fetchLockedUtxos, { immediate: true })
onMounted(fetchLockedUtxos)
</script>
