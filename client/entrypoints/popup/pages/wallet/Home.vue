<template>
  <LayoutHome class="">
    <WalletHeader
      :wallet-name="wallet.name"
      :network-id="wallet.networkId"
    />

    <AccountCard :account-name="wallet.accountName" :address="address" />

    <BalanceDisplay :balance="satsToBtc(balance)" />

    <ActionButtons
      @receive="handleReceive"
      @send="handleSend"
      @history="handleHistory"
      @buy="handleBuy"
    />
  </LayoutHome>
</template>

<script setup>
import { ref } from 'vue'
import LayoutHome from '@/components/layout/LayoutHome.vue'
import WalletHeader from '@/components/wallet/HomeHeader.vue'
import AccountCard from '@/components/wallet/AccountCard.vue'
import BalanceDisplay from '@/components/wallet/BalanceDisplay.vue'
import ActionButtons from '@/components/wallet/ActionButtons.vue'
import { useWalletStore, useAssetsStore } from '@/store'
import { useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import { generateMempoolUrl, satsToBtc } from '@/utils'
// 钱包数据
const { loading } = useAssets()
const assetStore = useAssetsStore()

const { balance } = storeToRefs(assetStore)
const router = useRouter()
const wallet = ref({
  name: 'HD Wallet #2',
  networkId: '1',
  currency: 'tBTC',
  accountName: 'Account 1',
  address: 'tb1pt...8zjep',
  balance: '0.00142709',
})
const walletStore = useWalletStore()
const { address, network } = storeToRefs(walletStore)
// 事件处理函数
const handleReceive = () => {
  console.log('Receive clicked')
  router.push('/wallet/receive')
}

const handleSend = () => {
  router.push('/wallet/send')
  console.log('Send clicked')
}

const historyUrl = computed(() =>
  generateMempoolUrl({
    path: `/address/${address.value}`,
    network: network.value,
  })
)
console.log('historyUrl', historyUrl.value)

const handleHistory = () => {
  chrome.tabs.create({ url: historyUrl.value })
}

const handleBuy = () => {
  console.log('Buy clicked')
}

const handleTabChange = (tabId) => {
  console.log('Tab changed to:', tabId)
}
</script>
