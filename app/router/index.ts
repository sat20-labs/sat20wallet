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

const checkPassword = async () => {
  const password = walletStorage.getValue('password')
  if (password) {
    const passwordTime = walletStorage.getValue('passwordTime')
    if (passwordTime) {
      const now = new Date().getTime()
      const timeDiff = now - passwordTime
      if (timeDiff > 5 * 60 * 1000) {
        await walletStorage.setValue('password', null)
        await walletStorage.setValue('locked', true)
      }
    }
  }
}

router.beforeEach(async (to: any,) => {
  const walletStore = useWalletStore()

  // 确保 walletStorage 已经从 localStorage 初始化
  await walletStorage.initializeState()

  const hasWallet = walletStorage.getValue('hasWallet')
  
  // 在初始化后再检查密码时效性
  await checkPassword()
  
  const password = walletStorage.getValue('password')
  const network = walletStorage.getValue('network')
  // 从 walletStorage 读取最新的锁定状态，确保与存储同步
  const isLocked = walletStorage.getValue('locked') ?? true

  if (password && isLocked) {
    await walletStore.unlockWallet(password)
    await walletStore.setLocked(false)
    // if (network) {
    //   console.log('network', network)
    //   await walletStore.setNetwork(network)
    // }
  }

  // 重新获取锁定状态，因为可能在 unlockWallet 中被更新
  const currentLocked = walletStorage.getValue('locked') ?? true

  if (to.path.startsWith('/wallet')) {
    if (hasWallet) {
      if (currentLocked) {
        return '/unlock?redirect=' + to.path
      }
    } else {
      return '/'
    }
  } else if (to.path === '/unlock') {
    // 如果用户已经解锁，重定向到钱包主页或指定页面
    if (hasWallet && !currentLocked) {
      const redirectPath = to.query.redirect as string
      return redirectPath || '/wallet'
    }
  } else {
    if (hasWallet) {
      if (currentLocked) {
        return '/unlock?redirect=' + to.path
      } else {
        return '/wallet'
      }
    }
  }
})

export default router
