import { createWebHashHistory, createRouter } from 'vue-router'
import { useWalletStore } from '@/store'
import Index from '@/entrypoints/popup/pages/Index.vue'
import ImportWallet from '@/entrypoints/popup/pages/Import.vue'
import CreateWallet from '@/entrypoints/popup/pages/Create.vue'
import WalletHome from '@/entrypoints/popup/pages/wallet/Home.vue'
import WalletSend from '@/entrypoints/popup/pages/wallet/Send.vue'
import WalletSetting from '@/entrypoints/popup/pages/wallet/Setting.vue'
import WalletReceive from '@/entrypoints/popup/pages/wallet/Receive.vue'
import WalletSettingPhrase from '@/entrypoints/popup/pages/wallet/settings/showPhrase.vue'
import Unlock from '@/entrypoints/popup/pages/Unlock.vue'
import Approve from '@/entrypoints/popup/pages/wallet/Approve.vue'
import { walletStorage } from '@/lib/walletStorage'
import { storage } from 'wxt/storage'

const routes = [
  { path: '/', component: Index },
  { path: '/import', component: ImportWallet },
  { path: '/create', component: CreateWallet },
  { path: '/unlock', component: Unlock },
  {
    path: '/wallet',
    children: [
      { path: '', component: WalletHome },
      {
        path: 'setting',
        children: [
          {
            path: '',
            component: WalletSetting,
          },
          {
            path: 'phrase',
            component: WalletSettingPhrase,
          },
        ],
      },
      { path: 'send', component: WalletSend },
      { path: 'receive', component: WalletReceive },
      { path: 'approve', component: Approve },
    ],
  },
]

const router = createRouter({
  history: createWebHashHistory(),
  routes,
})
router.beforeEach(async (to, from) => {
  const walletStore = useWalletStore()
  console.log('walletStore:', walletStore)

  const hasWallet = walletStorage.hasWallet
  const locked = walletStorage.locked
  console.log('walletStorage', walletStorage)
  console.log('walletStorage', walletStorage)
  console.log('walletStorage', walletStorage.hasWallet)

  console.log('hasWallet:', hasWallet)
  console.log('locked:', locked)
  const wallet = await storage.getItem('local:wallet_hasWallet')
  console.log('wallet:', wallet)

  if (to.path.startsWith('/wallet')) {
    if (hasWallet) {
      if (walletStore.locked) {
        return '/unlock?redirect=' + to.path
      }
    } else {
      return '/'
    }
  } else if (to.path === '/unlock') {
  } else {
    if (hasWallet) {
      if (walletStore.locked) {
        return '/unlock?redirect=' + to.path
      } else {
        return '/wallet'
      }
    }
  }
})

export default router
