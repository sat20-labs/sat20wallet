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
import DappMarket from '@/entrypoints/popup/pages/wallet/DappMarket.vue'
import Tools from '@/entrypoints/popup/pages/wallet/Tools.vue'
import AgentSignData from '@/entrypoints/popup/pages/wallet/AgentSignData.vue'
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
      { path: 'dapp', component: DappMarket },
      { path: 'agent-sign-data', component: AgentSignData },
      { path: 'tools', component: Tools },
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

let wasmWalletUnlocked = false

router.beforeEach(async (to: any,) => {
  const walletStore = useWalletStore()

  // 确保 walletStorage 已经从 localStorage 初始化
  await walletStorage.initializeState()

  const hasWallet = walletStorage.getValue('hasWallet')
  const password = walletStore.password
  // 从 walletStorage 读取最新的锁定状态，确保与存储同步
  const isLocked = walletStorage.getValue('locked') ?? true

  if (password && (isLocked || !wasmWalletUnlocked)) {
    const [error] = await walletStore.unlockWallet(password)
    if (error) {
      wasmWalletUnlocked = false
      await walletStore.setPassword('')
      await walletStore.setLocked(true)
    } else {
      wasmWalletUnlocked = true
      await walletStore.setLocked(false)
    }
    // if (network) {
    //   console.log('network', network)
    //   await walletStore.setNetwork(network)
    // }
  } else if (hasWallet && !password && !wasmWalletUnlocked) {
    await walletStore.setLocked(true)
  }

  // 重新获取锁定状态，因为可能在 unlockWallet 中被更新
  const currentLocked = walletStorage.getValue('locked') ?? true

  if (to.path.startsWith('/wallet')) {
    if (hasWallet) {
      if (currentLocked) {
        return { path: '/unlock', query: { redirect: to.fullPath } }
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
        return { path: '/unlock', query: { redirect: to.fullPath } }
      } else {
        return '/wallet'
      }
    }
  }
})

export default router
