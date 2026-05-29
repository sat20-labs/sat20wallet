import { createWebHashHistory, createRouter } from 'vue-router'
import { useWalletStore } from '@/store'
import Index from '@/entrypoints/popup/pages/Index.vue'
import ImportWallet from '@/entrypoints/popup/pages/Import.vue'
import CreateWallet from '@/entrypoints/popup/pages/Create.vue'
import WalletIndex from '@/entrypoints/popup/pages/wallet/index.vue'
import WalletSetting from '@/entrypoints/popup/pages/wallet/Setting.vue'
import WalletReceive from '@/entrypoints/popup/pages/wallet/Receive.vue'
import WalletSettingPhrase from '@/entrypoints/popup/pages/wallet/settings/phrase.vue'
import WalletSettingPublicKey from '@/entrypoints/popup/pages/wallet/settings/publickey.vue'
import WalletSettingPassword from '@/entrypoints/popup/pages/wallet/settings/password.vue'
import WalletSettingReferrer from '@/entrypoints/popup/pages/wallet/settings/referrer/index.vue'
import WalletSettingReferrerBind from '@/entrypoints/popup/pages/wallet/settings/referrer/bind.vue'
import NameSelect from '@/entrypoints/popup/pages/wallet/NameSelect.vue'

import WalletSettingNode from '@/entrypoints/popup/pages/wallet/settings/node.vue'
import WalletManager from '@/components/wallet/WalletManager.vue'
import SubWalletManager from '@/components/wallet/SubWalletManager.vue'
import Unlock from '@/entrypoints/popup/pages/Unlock.vue'
import Approve from '@/entrypoints/popup/pages/wallet/Approve.vue'
import UtxoManager from '@/entrypoints/popup/pages/wallet/settings/UtxoManager.vue'
import SplitAsset from '@/entrypoints/popup/pages/wallet/split.vue'
import { walletStorage } from '@/lib/walletStorage'

const routes = [
  { path: '/', component: Index },
  { path: '/import', component: ImportWallet },
  { path: '/create', component: CreateWallet },
  { path: '/unlock', component: Unlock },
  
  {
    path: '/wallet',
    children: [
      { path: '', component: WalletIndex },
      { path: 'name-select', component: NameSelect },
      { path: 'split-asset', component: SplitAsset },
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
          {
            path: 'publickey',
            component: WalletSettingPublicKey,
          },
          {
            path: 'password',
            component: WalletSettingPassword,
          },
          {
            path: 'utxo',
            component: UtxoManager,
          },
          {
            path: 'node',
            component: WalletSettingNode,
          },
          {
            path: 'referrer/register',
            component: WalletSettingReferrer,
          },
          {
            path: 'referrer/bind',
            component: WalletSettingReferrerBind,
          },
        ],
      },
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

router.beforeEach(async (to) => {
  console.time('router.beforeEach')
  const walletStore = useWalletStore()

  const hasWallet = walletStorage.getValue('hasWallet')
  const password = walletStore.password

  if (password && walletStore.locked) {
    const [err] = await walletStore.unlockWallet(password)
    if (err) {
      await walletStore.setPassword('')
      await walletStore.setLocked(true)
    } else {
      await walletStore.setLocked(false)
    }
  }

  if (to.path.startsWith('/wallet')) {
    if (hasWallet) {
      if (walletStore.locked) {
        console.timeEnd('router.beforeEach')
        return { path: '/unlock', query: { redirect: to.fullPath } }
      }
    } else {
      console.timeEnd('router.beforeEach')
      return '/'
    }
  } else if (to.path === '/unlock') {
    if (hasWallet && !walletStore.locked) {
      console.timeEnd('router.beforeEach')
      return (to.query.redirect as string) || '/wallet'
    }
  } else {
    if (hasWallet) {
      if (walletStore.locked) {
        console.timeEnd('router.beforeEach')
        return { path: '/unlock', query: { redirect: to.fullPath } }
      } else {
        console.timeEnd('router.beforeEach')
        return '/wallet'
      }
    }
  }
  console.timeEnd('router.beforeEach')
})

export default router
