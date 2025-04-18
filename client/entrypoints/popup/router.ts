import { createWebHashHistory, createRouter } from 'vue-router'
import { useWalletStore } from '@/store'
import Index from '@/entrypoints/popup/pages/Index.vue'
import ImportWallet from '@/entrypoints/popup/pages/Import.vue'
import CreateWallet from '@/entrypoints/popup/pages/Create.vue'
import WalletIndex from '@/entrypoints/popup/pages/wallet/index.vue'
import WalletAsset from '@/entrypoints/popup/pages/wallet/asset.vue'
import WalletSetting from '@/entrypoints/popup/pages/wallet/Setting.vue'
import WalletReceive from '@/entrypoints/popup/pages/wallet/Receive.vue'
import WalletSettingPhrase from '@/entrypoints/popup/pages/wallet/settings/phrase.vue'
import WalletL2Send from '@/entrypoints/popup/pages/wallet/l2/send.vue'
import WalletManager from '@/components/wallet/WalletManager.vue'
import SubWalletManager from '@/components/wallet/SubWalletManager.vue'
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
      { path: '', component: WalletIndex },
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
      {
        path: 'l2',
        children: [
          {
            path: 'send',
            component: WalletL2Send,
          },
        ],
      },
      { path: 'asset', component: WalletAsset },
      { path: 'receive', component: WalletReceive },
      { path: 'approve', component: Approve },
      { path: 'manager', component: WalletManager },
      { path: 'sub-wallet-manager', component: SubWalletManager },
    ],
  },
]

const router = createRouter({
  history: createWebHashHistory(),
  routes,
})

const checkPassword = async () => {
  const password = walletStorage.getValue('password')
  console.log('password', password)

  if (password) {
    const passwordTime = walletStorage.getValue('passwordTime')
    console.log('passwordTime', passwordTime)
    if (passwordTime) {
      const now = new Date().getTime()
      if (now - passwordTime > 5 * 60 * 1000) {
        await walletStorage.setValue('password', null)
      }
    }
  }
}

router.beforeEach(async (to, from) => {
  const walletStore = useWalletStore()

  const hasWallet = walletStorage.getValue('hasWallet')
  await checkPassword()
  const password = walletStorage.getValue('password')

  if (password) {
    await walletStore.unlockWallet(password)
    await walletStore.setLocked(false)
  }

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
