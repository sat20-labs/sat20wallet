import { defineStore } from 'pinia'
import { walletStorage } from '@/lib/walletStorage'
import { Network, Chain, WalletData, WalletAccount, WalletType } from '@/types'
import walletManager from '@/utils/sat20'
import satsnetStp from '@/utils/stp'
import { useChannelStore } from './channel'
import { ref, computed, toRaw } from 'vue'
import { getConfig, logLevel } from '@/config/wasm'
import { sendNetworkChangedEvent, sendAccountsChangedEvent } from '@/lib/utils'


export const useWalletStore = defineStore('wallet', () => {
  const channelStore = useChannelStore()

  const address = ref(walletStorage.getValue('address'))
  const publicKey = ref(walletStorage.getValue('pubkey'))
  const walletId = ref(walletStorage.getValue('walletId'))
  const accountIndex = ref(walletStorage.getValue('accountIndex'))
  const feeRate = ref(0)
  const btcFeeRate = ref(1)
  const satsnetFeeRate = ref(10)
  const password = ref(walletStorage.getValue('password'))
  const network = ref(walletStorage.getValue('network'))
  const chain = ref(walletStorage.getValue('chain'))
  const locked = ref(true)
  const hasWallet = ref(!!walletStorage.getValue('hasWallet'))
  const localWallets = walletStorage.getValue('wallets');
  const wallets = ref<WalletData[]>(localWallets ? JSON.parse(JSON.stringify(localWallets)) : [])

  // 迁移历史钱包数据：为没有 walletType 的钱包设置默认类型为 MNEMONIC
  const migrateHistoricalWallets = async () => {
    let needsUpdate = false
    const updatedWallets = wallets.value.map(wallet => {
      if (!wallet.walletType) {
        needsUpdate = true
        return {
          ...wallet,
          walletType: WalletType.MNEMONIC
        }
      }
      return wallet
    })

    if (needsUpdate) {
      wallets.value = updatedWallets
      await walletStorage.setValue('wallets', toRaw(wallets.value))
      console.log('Historical wallets migrated to include walletType')
    }
  }

  // 立即执行迁移
  migrateHistoricalWallets()

  // 添加全局切换状态管理
  const isSwitchingWallet = ref(false)
  const isSwitchingAccount = ref(false)
  console.log(wallets);
  console.log(walletId);
  const wallet = computed(() => wallets.value.find(w => w.id === walletId.value))
  const accounts = computed(() => wallet.value?.accounts)
  const account = computed(() => wallet.value?.accounts?.find(a => a.index === accountIndex.value))

  // 当前钱包类型，默认为助记词类型（兼容历史数据）
  const currentWalletType = computed(() => wallet.value?.walletType || WalletType.MNEMONIC)

  // 安全的账户变更事件发送函数
  const safeSendAccountsChangedEvent = async (accountsData: any) => {
    try {
      await sendAccountsChangedEvent(accountsData)
    } catch (error) {
      console.warn('sendAccountsChangedEvent failed:', error)
      // 不中断主流程，仅记录警告
    }
  }
  const setAddress = async (value: string) => {
    address.value = value
    await walletStorage.setValue('address', value)
  }

  const setWalletId = async (value: string) => {
    await walletStorage.setValue('walletId', value)
    walletId.value = value
  }

  const setAccountIndex = async (value: number) => {
    await walletStorage.setValue('accountIndex', value)
    accountIndex.value = value
  }

  const setPublickey = async (value: string) => {
    await walletStorage.setValue('pubkey', value)
    publicKey.value = value
  }
  const setBtcFeeRate = async (value: number) => {
    btcFeeRate.value = value
  }
  const setSatsnetFeeRate = async (value: number) => {
    satsnetFeeRate.value = value
  }
  const setPassword = async (value: string) => {
    await walletStorage.updatePassword(value)
    password.value = value
  }

  const setNetwork = async (value: Network) => {
    const n = value === Network.LIVENET ? 'mainnet' : 'testnet'
    const [err] = await walletManager.switchChain(n, password.value as string)

    await walletStorage.setValue('network', value)
    network.value = value
    const [_, addressRes] = await walletManager.getWalletAddress(accountIndex.value)
    const [__, pubkeyRes] = await walletManager.getWalletPubkey(accountIndex.value)
    if (addressRes && pubkeyRes) {
      const { address } = addressRes
      await setAddress(address)
      await setPublickey(pubkeyRes.pubKey)
    }
    const env = walletStorage.getValue('env') || 'test'
    const config = getConfig(env, value)

    await satsnetStp.release()
    await walletManager.release()
    await satsnetStp.init(config, logLevel)
    await walletManager.init(config, logLevel)
    try {
      console.log(`Sending NETWORK_CHANGED message with payload: ${value}`)
      await sendNetworkChangedEvent(value)
    } catch (error) {
      console.error('Failed to send NETWORK_CHANGED message to background:', error)
    }
  }

  const setChain = async (value: Chain) => {
    await walletStorage.setValue('chain', value)
    chain.value = value
  }

  const setLocked = async (value: boolean) => {
    await walletStorage.setValue('locked', value)
    locked.value = value
  }

  const setHasWallet = async (value: boolean) => {
    await walletStorage.setValue('hasWallet', value)
    hasWallet.value = value
  }

  const setFeeRate = (value: number) => {
    feeRate.value = value
  }
  const switchWallet = async (walletIdToSwitch: string) => {
    // 如果正在切换，直接返回
    if (isSwitchingWallet.value) {
      console.log('Wallet switch already in progress, ignoring...')
      return
    }

    try {
      isSwitchingWallet.value = true
      console.log('Starting wallet switch to:', walletIdToSwitch)

      await walletManager.switchWallet(walletIdToSwitch, password.value as string)
      await satsnetStp.switchWallet(walletIdToSwitch, password.value as string)
      const currentAccount = wallets.value.find(w => w.id === walletIdToSwitch)?.accounts[0];
      await setWalletId(walletIdToSwitch);
      await switchToAccount(currentAccount?.index || 0);
      await getWalletInfo()

      console.log('Wallet switch completed successfully')
    } catch (error) {
      console.error('Wallet switch failed:', error)
      throw error
    } finally {
      isSwitchingWallet.value = false
    }
  }
  const createWallet = async (password: string) => {
    const [err, res] = await walletManager.createWallet(password)
    if (err || !res) {
      console.error(err)
      return [err, undefined]
    }
    const { walletId, mnemonic: _mnemonic } = res
    await setWalletId(walletId)
    await setAccountIndex(0)
    await setHasWallet(true)
    await setLocked(false)
    await setChain(Chain.BTC)
    await satsnetStp.importWallet(_mnemonic, password)
    await setPassword(password)
    await satsnetStp.start()
    await channelStore.getAllChannels()
    const [_e, addressRes] = await walletManager.getWalletAddress(
      accountIndex.value
    )
    const [_j, pubkeyRes] = await walletManager.getWalletPubkey(
      accountIndex.value
    )

    if (addressRes && pubkeyRes) {
      const { address } = addressRes
      await setAddress(address)
      await setPublickey(pubkeyRes.pubKey)
      const _wallets = JSON.parse(JSON.stringify(walletStorage.getValue('wallets')))
      const walletLen = _wallets.length
      _wallets.push({
        id: walletId,
        name: `Wallet ${walletLen + 1}`,
        walletType: WalletType.MNEMONIC,
        accounts: [{
          index: 0,
          name: `Account ${0 + 1}`,
          address: address,
          pubKey: pubkeyRes.pubKey
        }]
      })
      wallets.value = _wallets
      console.log('createWallet', _wallets);

      await walletStorage.setValue('wallets', _wallets)
    }
    return [undefined, _mnemonic]
  }

  const importWallet = async (mnemonic: string, password: string) => {
    // 助记词预处理：去掉前后空格和其他符号，末尾只允许英文字符
    const cleanMnemonic = (rawMnemonic: string): string => {
      let cleaned = rawMnemonic.trim()

      cleaned = cleaned.replace(/[^a-zA-Z0-9\s]/g, '')

      cleaned = cleaned.replace(/\s+/g, ' ')

      cleaned = cleaned.trim()

      cleaned = cleaned.replace(/\s+$/, '')

      return cleaned
    }

    const processedMnemonic = cleanMnemonic(mnemonic)

    const [err, res] = await walletManager.importWallet(processedMnemonic, password)
    if (err || !res) {
      console.error(err)
      return [err, undefined]
    }
    const { walletId } = res
    await setWalletId(walletId)
    await setAccountIndex(0)
    await setHasWallet(true)
    await setLocked(false)
    // await setNetwork(Network.TESTNET)
    await setChain(Chain.BTC)
    await satsnetStp.importWallet(processedMnemonic, password)
    await setPassword(password)
    await satsnetStp.start()
    await channelStore.getAllChannels()
    const [_e, addressRes] = await walletManager.getWalletAddress(
      accountIndex.value
    )
    const [_j, pubkeyRes] = await walletManager.getWalletPubkey(
      accountIndex.value
    )
    const _wallets = JSON.parse(JSON.stringify(walletStorage.getValue('wallets')))
    if (addressRes && pubkeyRes) {
      const { address } = addressRes
      await setAddress(address)
      await setPublickey(pubkeyRes.pubKey)
      const walletLen = _wallets.length
      _wallets.push({
        id: walletId,
        name: `Wallet ${walletLen + 1}`,
        walletType: WalletType.MNEMONIC,
        accounts: [{
          index: 0,
          name: `Account ${0 + 1}`,
          address: address,
          pubKey: pubkeyRes.pubKey
        }]
      })
    }
    wallets.value = _wallets
    await walletStorage.setValue('wallets', _wallets)
    console.log('importWallet', _wallets);
    console.log('wallet id', walletId);

    return [undefined, processedMnemonic]
  }

  const importWalletWithPrivKey = async (privateKey: string, password: string): Promise<[Error | undefined, boolean | undefined]> => {
    const [err, res] = await walletManager.importWalletWithPrivKey(privateKey, password)
    if (err || !res) {
      console.error(err)
      return [err, undefined]
    }
    const { walletId } = res
    const walletIdStr = walletId.toString()
    await setWalletId(walletIdStr)
    await setAccountIndex(0)
    await setHasWallet(true)
    await setLocked(false)
    await setChain(Chain.BTC)
    await satsnetStp.importWalletWithPrivKey(privateKey, password)
    await setPassword(password)
    await satsnetStp.start()

    await channelStore.getAllChannels()
    const [_e, addressRes] = await walletManager.getWalletAddress(
      accountIndex.value
    )
    const [_j, pubkeyRes] = await walletManager.getWalletPubkey(
      accountIndex.value
    )
    const _wallets = JSON.parse(JSON.stringify(walletStorage.getValue('wallets')))
    if (addressRes && pubkeyRes) {
      const { address } = addressRes
      await setAddress(address)
      await setPublickey(pubkeyRes.pubKey)
      const walletLen = _wallets.length
      _wallets.push({
        id: walletIdStr,
        name: `Wallet ${walletLen + 1}`,
        walletType: WalletType.PRIVATE_KEY,
        accounts: [{
          index: 0,
          name: `Account ${0 + 1}`,
          address: address,
          pubKey: pubkeyRes.pubKey
        }]
      })
    }
    wallets.value = _wallets
    await walletStorage.setValue('wallets', _wallets)

    return [undefined, true]
  }

  const createMonitorWallet = async (address: string): Promise<[Error | undefined, boolean | undefined]> => {
    const [err, res] = await walletManager.createMonitorWallet(address)
    if (err || !res) {
      console.error(err)
      return [err, undefined]
    }
    const { walletId } = res
    const walletIdStr = walletId.toString()
    await setWalletId(walletIdStr)
    await setAccountIndex(0)
    await setHasWallet(true)
    await setLocked(false)
    await setChain(Chain.BTC)
    // Monitor wallet might not have password

    // For monitor wallet, we might not be able to use STP fully if it requires signing?
    // But we can try to start it? Or maybe it doesn't support monitor wallets yet?
    // User only asked to add the interface.

    const [_e, addressRes] = await walletManager.getWalletAddress(
      accountIndex.value
    )
    // Monitor wallet, address is what we passed, but let's confirm with getWalletAddress if it works
    // If it's a monitor wallet, maybe we don't have pubkey in the same way?
    // But let's try to get what we can.

    const _wallets = JSON.parse(JSON.stringify(walletStorage.getValue('wallets')))

    // If getWalletAddress works
    if (addressRes) {
      const { address: returnedAddress } = addressRes
      await setAddress(returnedAddress)
      // monitor wallet might not have pubkey available if not derived?
      // but typically watch-only imports address directly.

      const walletLen = _wallets.length
      _wallets.push({
        id: walletIdStr,
        name: `Monitor Wallet ${walletLen + 1}`,
        walletType: WalletType.MONITOR,
        accounts: [{
          index: 0,
          name: `Account ${0 + 1}`,
          address: returnedAddress,
          pubKey: '' // Monitor wallet might not have pubkey known
        }]
      })
    } else {
      // Fallback if getWalletAddress fails (shouldn't if wallet created)
      await setAddress(address)
      const walletLen = _wallets.length
      _wallets.push({
        id: walletIdStr,
        name: `Monitor Wallet ${walletLen + 1}`,
        walletType: WalletType.MONITOR,
        accounts: [{
          index: 0,
          name: `Account ${0 + 1}`,
          address: address,
          pubKey: ''
        }]
      })
    }

    wallets.value = _wallets
    await walletStorage.setValue('wallets', _wallets)

    return [undefined, true]
  }
  const getWalletInfo = async () => {
    const [_e, addressRes] = await walletManager.getWalletAddress(
      accountIndex.value
    )
    const [_j, pubkeyRes] = await walletManager.getWalletPubkey(
      accountIndex.value
    )

    if (addressRes && pubkeyRes) {
      const { address } = addressRes
      await setAddress(address)
      await setPublickey(pubkeyRes.pubKey)
    }
  }

  const unlockWallet = async (password: string) => {
    const [err, result] = await walletManager.unlockWallet(password)
    console.log('unlockWallet', err, result)
    if (!err && result) {
      const { walletId: unlockedWalletId } = result
      // await setWalletId(unlockedWalletId)
      await getWalletInfo()
      await setLocked(false)
      await setPassword(password)
      await satsnetStp.unlockWallet(password)
      await satsnetStp.start()
      await switchToAccount(accountIndex.value, false)
      await channelStore.getAllChannels()
    }
    return [err, result]
  }

  const deleteWallet = async (walletIdToDelete: string) => {
    try {
      const walletIndexToDelete = wallets.value.findIndex(w => w.id === walletIdToDelete);

      if (walletIndexToDelete === -1) {
        const errMsg = `Wallet with ID ${walletIdToDelete} not found.`
        console.error(errMsg);
        return [new Error(errMsg), undefined];
      }

      const isDeletingActiveWallet = walletIdToDelete === walletId.value;

      wallets.value.splice(walletIndexToDelete, 1);

      if (wallets.value.length === 0) {
        await walletStorage.clear();
        await setHasWallet(false);
        await setAddress('');
        await setPublickey('');
        await setWalletId('');
        await setAccountIndex(0);
        await setPassword('');
        await setChain(Chain.BTC);
        await setLocked(true);
      } else {
        await walletStorage.setValue('wallets', toRaw(wallets.value));

        if (isDeletingActiveWallet) {
          const nextWalletIndex = walletIndexToDelete > 0 ? walletIndexToDelete - 1 : 0;
          const nextWallet = wallets.value[nextWalletIndex];

          if (nextWallet && nextWallet.accounts.length > 0) {
            const nextAccount = nextWallet.accounts[0];
            await setWalletId(nextWallet.id);
            await switchToAccount(nextAccount.index);
          } else {
            const errMsg = "Failed to switch wallet: Next wallet or its accounts not found after deletion."
            console.error(errMsg);
            return [new Error(errMsg), undefined];
          }
        }
      }

      return [undefined, true];
    } catch (error: any) {
      console.error('Failed to delete wallet:', error);
      return [error, undefined];
    }
  }

  const addAccount = async (name: string, accountId: number) => {
    await walletManager.switchAccount(accountId)
    await satsnetStp.switchAccount(accountId)
    const [_, addressRes] = await walletManager.getWalletAddress(accountId)
    const [__, pubkeyRes] = await walletManager.getWalletPubkey(accountId)
    if (addressRes && pubkeyRes) {
      const newAccount: WalletAccount = {
        index: accountId,
        name,
        address: addressRes.address,
        pubKey: pubkeyRes.pubKey
      }

      // 使用响应式的 wallets.value 而不是重新从 storage 读取，确保状态一致
      const walletToUpdate = wallets.value.find(w => w.id === walletId.value)
      console.log('walletToUpdate', walletToUpdate);
      if (!walletToUpdate) {
        console.error('addAccount: Current wallet not found', walletId.value)
        return
      }
      console.log('walletToUpdate.accounts', walletToUpdate.accounts);
      if (!Array.isArray(walletToUpdate.accounts)) {
        console.warn('addAccount: wallet.accounts is not an array, initializing...', walletToUpdate.accounts)
        walletToUpdate.accounts = []
      }

      walletToUpdate.accounts.push(newAccount)

      // 保存到存储
      await walletStorage.setValue('wallets', toRaw(wallets.value))

      await setAccountIndex(accountId)
      await setAddress(addressRes.address)
      await setPublickey(pubkeyRes.pubKey)

      console.log('addAccount success:', addressRes.address);
    }
  }

  const switchToAccount = async (accountId: number, emitEvent: boolean = true) => {
    // 如果正在切换账户，直接返回
    if (isSwitchingAccount.value) {
      console.log('Account switch already in progress, ignoring...')
      return
    }

    try {
      isSwitchingAccount.value = true
      console.log('Starting account switch to:', accountId)

      await walletManager.switchAccount(accountId)
      await satsnetStp.switchAccount(accountId)
      const [_, addressRes] = await walletManager.getWalletAddress(accountId)
      const [__, pubkeyRes] = await walletManager.getWalletPubkey(accountId)

      const currentAddress = address.value

      if (addressRes && pubkeyRes) {
        await setAccountIndex(accountId)
        await setAddress(addressRes.address)
        await setPublickey(pubkeyRes.pubKey)
      }

      // 只有在满足以下条件时才发送账户变更事件：
      // 1. emitEvent 为 true
      // 2. 新地址存在
      // 3. 旧地址存在（如果是 null，说明是窗口首次加载，不应视为变更）
      // 4. 地址真正发生了变化（忽略大小写，防止 Bech32 地址大小写不一致导致的循环）
      if (
        emitEvent &&
        address.value &&
        currentAddress &&
        address.value.toLowerCase() !== currentAddress.toLowerCase()
      ) {
        safeSendAccountsChangedEvent([address.value])
      }

      console.log('Account switch completed successfully')
    } catch (error) {
      console.error('Account switch failed:', error)
      throw error
    } finally {
      isSwitchingAccount.value = false
    }
  }

  const updateAccountName = async (accountId: number, newName: string) => {
    if (account) {
      const wallet = wallets.value.find(w => w.id === walletId.value)
      const account = wallet?.accounts?.find(a => a.index === accountId)
      if (account) {
        account.name = newName
      }
      await walletStorage.setValue('wallets', toRaw(wallets.value))
    }
  }

  const updateWalletName = async (walletId: string, newName: string) => {
    const wallet = wallets.value.find(w => w.id === walletId)
    if (wallet) {
      wallet.name = newName
      await walletStorage.setValue('wallets', toRaw(wallets.value))
    }
  }

  const deleteAccount = async (accountId: number) => {
    const parentWallet = wallets.value.find(w => w.id === walletId.value)
    if (!parentWallet) return

    const index = parentWallet.accounts.findIndex(a => a.index === accountId)
    if (index !== -1) {
      parentWallet.accounts.splice(index, 1)
      await walletStorage.setValue('wallets', toRaw(wallets.value))

      // 如果删除的是当前选中的账户，或者当前选中的账户不再存在，切换到上一个账号
      if (accountIndex.value === accountId || !parentWallet.accounts.find(a => a.index === accountIndex.value)) {
        const prevAccount = parentWallet.accounts[Math.max(0, index - 1)]
        if (prevAccount) {
          await switchToAccount(prevAccount.index)
        }
      }
    }
  }
  console.log('wallets', wallets);
  console.log('wallet', wallet);
  console.log('accounts', accounts);
  console.log('account', account);
  console.log('walletid', walletId);


  return {
    address,
    setAddress,
    walletId,
    setWalletId,
    network,
    setNetwork,
    chain,
    setChain,
    locked,
    setLocked,
    hasWallet,
    setHasWallet,
    publicKey,
    setPublickey,
    feeRate,
    setFeeRate,
    accountIndex,
    setAccountIndex,
    createWallet,
    importWallet,
    importWalletWithPrivKey,
    createMonitorWallet,
    getWalletInfo,
    deleteWallet,
    password,
    setPassword,
    unlockWallet,
    wallets,
    wallet,
    addAccount,
    switchToAccount,
    updateAccountName,
    updateWalletName,
    deleteAccount,
    accounts,
    switchWallet,
    btcFeeRate,
    setBtcFeeRate,
    satsnetFeeRate,
    setSatsnetFeeRate,
    // 导出切换状态
    isSwitchingWallet,
    isSwitchingAccount,
    // 当前钱包类型
    currentWalletType,
  }
})
