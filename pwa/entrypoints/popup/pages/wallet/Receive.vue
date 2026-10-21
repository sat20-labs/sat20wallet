<template>
  <LayoutSecond title="Receive">
    <div class="flex flex-col gap-5 items-center">
      <div v-if="isBitcoin" class="w-full flex gap-2" role="group" aria-label="BTC receiving method">
        <button class="flex-1 px-3 py-2 rounded-lg border text-sm"
          :class="mode === 'normal' ? 'border-primary text-primary' : 'border-zinc-700'"
          type="button" @click="mode = 'normal'">Ordinary BTC</button>
        <button class="flex-1 px-3 py-2 rounded-lg border text-sm"
          :class="mode === 'silent' ? 'border-primary text-primary' : 'border-zinc-700'"
          type="button" @click="mode = 'silent'">Private · SP</button>
      </div>

      <template v-if="mode === 'normal' || !isBitcoin">
        <QrcodeVue :value="address" :size="200" class="mx-auto" v-if="address" />
        <div class="w-60">
          <CopyCard :text="address" v-if="address"><div>{{ hideAddress(address) }}</div></CopyCard>
        </div>
      </template>
      <template v-else>
        <p class="text-center text-sm text-zinc-400">
          BIP352 Silent Payments uses a reusable private receiving address.
          The sender's wallet must support SP.
        </p>
        <p v-if="loading" role="status" class="text-sm text-zinc-400">Reading SP account…</p>
        <p v-if="error" role="alert" class="text-sm text-red-400 break-words">{{ error }}</p>
        <template v-if="enabled && spAddress">
          <QrcodeVue :value="spAddress" :size="200" class="mx-auto" />
          <div class="w-64">
            <CopyCard :text="spAddress"><div class="break-all text-xs">{{ hideAddress(spAddress) }}</div></CopyCard>
          </div>
          <button type="button" class="rounded-lg border border-zinc-600 px-4 py-2 text-sm"
            :disabled="syncing" @click="scanPayments">
            {{ syncing ? 'Scanning verified BTC outputs…' : 'Refresh private BTC balance' }}
          </button>
          <div v-if="balanceComplete" class="text-center text-sm">
            <div>SP balance (confirmed snapshot)</div>
            <strong>{{ (balanceSats / 100000000).toFixed(8) }} BTC</strong>
            <p class="text-xs text-zinc-500">This is the separate SP recovery view; full transaction history may still be syncing.</p>
          </div>
          <p v-else class="text-center text-xs text-zinc-400">
            Private BTC balance not scanned yet; zero must not be assumed.
          </p>
        </template>
        <button v-else-if="!loading && !enabled" type="button"
          class="rounded-lg border border-primary px-4 py-2 text-sm"
          :disabled="enabling" @click="enablePrivateReceive">
          {{ enabling ? 'Enabling…' : 'Enable private BTC receive for this account' }}
        </button>
      </template>
    </div>
  </LayoutSecond>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import QrcodeVue from 'qrcode.vue'
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import CopyCard from '@/components/common/CopyCard.vue'
import { hideAddress } from '@/utils'
import walletManager from '@/utils/sat20'
import { useWalletStore } from '@/store'

const walletStore = useWalletStore()
const { address, walletId, accountIndex, chain } = storeToRefs(walletStore)
const isBitcoin = computed(() => String(chain.value).toLowerCase() === 'btc')
const mode = ref<'normal' | 'silent'>('normal')
const loading = ref(false)
const enabling = ref(false)
const syncing = ref(false)
const enabled = ref(false)
const spAddress = ref('')
const balanceComplete = ref(false)
const balanceSats = ref(0)
const error = ref('')
let version = 0

watch([walletId, accountIndex, chain], async () => {
  const current = ++version
  enabled.value = false
  spAddress.value = ''
  balanceComplete.value = false
  balanceSats.value = 0
  error.value = ''
  if (!isBitcoin.value || !walletId.value) return
  loading.value = true
  try {
    const id = String(walletId.value)
    const index = Number(accountIndex.value ?? 0)
    const [accountError, account] = await walletManager.getSilentPayAccount(id, index)
    if (current !== version) return
    if (accountError) throw accountError
    enabled.value = Boolean(account?.enabled)
    spAddress.value = account?.address ?? ''
    if (enabled.value) {
      const [balanceError, balance] = await walletManager.getSilentPayBalance(id, index)
      if (current !== version) return
      // Missing stored balance is not zero. It only means recovery is pending.
      if (!balanceError && balance?.balance?.complete) {
        balanceComplete.value = true
        balanceSats.value = Number(balance.balance.balance)
      }
    }
  } catch (cause) {
    if (current === version) error.value = String(cause)
  } finally {
    if (current === version) loading.value = false
  }
}, { immediate: true })

const enablePrivateReceive = async () => {
  if (!walletId.value || enabling.value) return
  const current = version
  enabling.value = true
  error.value = ''
  try {
    const [err, account] = await walletManager.enableSilentPay(String(walletId.value), Number(accountIndex.value ?? 0))
    if (current !== version) return
    if (err) throw err
    enabled.value = Boolean(account?.enabled)
    spAddress.value = account?.address ?? ''
    await walletStore.syncWalletCatalog()
  } catch (cause) {
    if (current === version) error.value = String(cause)
  } finally {
    enabling.value = false
  }
}

const scanPayments = async () => {
  if (!walletId.value || syncing.value || !enabled.value) return
  const current = version
  syncing.value = true
  error.value = ''
  try {
    const [err, response] = await walletManager.syncSilentPayLive()
    if (current !== version) return
    if (err) throw err
    const account = response?.accounts.find(item =>
      String(item.wallet_id) === String(walletId.value) &&
      Number(item.account_index) === Number(accountIndex.value ?? 0))
    if (!account?.complete) throw new Error('SP account balance was not fully scanned')
    balanceSats.value = Number(account.balance)
    balanceComplete.value = true
  } catch (cause) {
    if (current === version) error.value = String(cause)
  } finally {
    syncing.value = false
  }
}
</script>
