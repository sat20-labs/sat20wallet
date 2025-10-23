import { defineStore } from 'pinia'
import { walletStorage } from '@/lib/walletStorage'
import { Network, Chain, WalletData, WalletAccount } from '@/types'
import walletManager from '@/utils/sat20'
import { ref, computed, toRaw } from 'vue'
import { sendNetworkChangedEvent, sendAccountsChangedEvent } from '@/lib/utils'


export const useWalletStore = defineStore('wallet', () => {
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
  const locked = ref(walletStorage.getValue('locked') ?? true)
  const hasWallet = ref(!!walletStorage.getValue('hasWallet'))
  const localWallets = walletStorage.getValue('wallets');
  const wallets = ref<WalletData[]>(localWallets ? structuredClone(localWallets) : [])

  // 添加全局切换状态管理
  const isSwitchingWallet = ref(false)
  const isSwitchingAccount = ref(false)

  // 监听 walletStorage 状态变化，同步到 walletStore
  walletStorage.subscribe((key, newValue, oldValue) => {
    switch (key) {
      case 'locked':
        locked.value = newValue ?? true
        break
      case 'password':
        password.value = newValue
        break
      case 'address':
        address.value = newValue
        break
      case 'pubkey':
        publicKey.value = newValue
        break
      case 'walletId':
        walletId.value = newValue
        break
      case 'accountIndex':
        accountIndex.value = newValue
        break
      case 'network':
        network.value = newValue
        break
      case 'chain':
        chain.value = newValue
        break
      case 'hasWallet':
        hasWallet.value = !!newValue
        break
      case 'wallets':
        wallets.value = newValue ? structuredClone(newValue) : []
        break
    }
  })
  const wallet = computed(() => wallets.value.find(w => w.id === walletId.value))
  const accounts = computed(() => wallet.value?.accounts)
  const account = computed(() => wallet.value?.accounts.find(a => a.index === accountIndex.value))

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

    try {
      console.log(`Sending NETWORK_CHANGED message with payload: ${value}`)
      await sendNetworkChangedEvent(value)
    } catch (error) {
      console.error('Failed to send NETWORK_CHANGED message to background:', error)
    }
    return true;
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
      const currentAccount = wallets.value.find(w => w.id === walletIdToSwitch)?.accounts[0];
      await setWalletId(walletIdToSwitch);
      await switchToAccount(currentAccount?.index || 0);
      await getWalletInfo()

      // 发送账户变更事件（非关键操作）
      safeSendAccountsChangedEvent(wallets.value)

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
    await setPassword(password)
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
      const _wallets = structuredClone(walletStorage.getValue('wallets'))
      const walletLen = _wallets.length
      _wallets.push({
        id: walletId,
        name: `Wallet ${walletLen + 1}`,
        accounts: [{
          index: 0,
          name: `Account ${0 + 1}`,
          address: address,
          pubKey: pubkeyRes.pubKey
        }]
      })
      wallets.value = _wallets
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
    await setPassword(password)
    const [_e, addressRes] = await walletManager.getWalletAddress(
      accountIndex.value
    )
    const [_j, pubkeyRes] = await walletManager.getWalletPubkey(
      accountIndex.value
    )
    const _wallets = structuredClone(walletStorage.getValue('wallets'))
    if (addressRes && pubkeyRes) {
      const { address } = addressRes
      await setAddress(address)
      await setPublickey(pubkeyRes.pubKey)
      const walletLen = _wallets.length
      _wallets.push({
        id: walletId,
        name: `Wallet ${walletLen + 1}`,
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
    return [undefined, processedMnemonic]
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

    // 检查是否是"钱包已解锁"的情况，这种情况下应该视为成功
    const isAlreadyUnlocked = err && (
      err.message && err.message.includes('wallet has been unlocked') ||
      err.toString().includes('wallet has been unlocked')
    )

    if (!err && result) {
      // 正常解锁成功
      await getWalletInfo()
      await setLocked(false)
      await setPassword(password)
      await switchToAccount(accountIndex.value)
      return [undefined, result]
    } else if (isAlreadyUnlocked) {
      // 钱包已经解锁，但前端状态可能是锁定的，需要同步状态
      console.log('检测到钱包已解锁，同步前端状态')
      await getWalletInfo()
      await setLocked(false)
      await setPassword(password)
      await switchToAccount(accountIndex.value)
      // 返回成功，不返回错误
      return [undefined, { alreadyUnlocked: true, message: '钱包已解锁，状态已同步' }]
    }

    // 真正的错误情况
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
    const [_, addressRes] = await walletManager.getWalletAddress(accountId)
    const [__, pubkeyRes] = await walletManager.getWalletPubkey(accountId)
    if (addressRes && pubkeyRes) {
      const newAccount: WalletAccount = {
        index: accountId,
        name,
        address: addressRes.address,
        pubKey: pubkeyRes.pubKey
      }
      const _wallets = structuredClone(walletStorage.getValue('wallets'))
      const _wallet = _wallets?.find((w: any) => w.id === walletId.value)
      if (_wallet) {
        _wallet.accounts.push(newAccount)
      }
      wallets.value = _wallets
      await walletStorage.setValue('wallets', _wallets)
      await setAccountIndex(accountId)
      await setAddress(addressRes.address)
      await setPublickey(pubkeyRes.pubKey)
    }
  }

  const switchToAccount = async (accountId: number) => {
    // 如果正在切换账户，直接返回
    if (isSwitchingAccount.value) {
      console.log('Account switch already in progress, ignoring...')
      return
    }

    try {
      isSwitchingAccount.value = true
      console.log('Starting account switch to:', accountId)

      await walletManager.switchAccount(accountId)
      const [_, addressRes] = await walletManager.getWalletAddress(accountId)
      const [__, pubkeyRes] = await walletManager.getWalletPubkey(accountId)
      if (addressRes && pubkeyRes) {
        await setAccountIndex(accountId)
        await setAddress(addressRes.address)
        await setPublickey(pubkeyRes.pubKey)
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
      const account = wallet?.accounts.find(a => a.index === accountId)
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
    const index = wallet.value?.accounts.findIndex(a => a.index === accountId)
    if (index && index > -1) {
      wallet.value?.accounts.splice(index, 1)
      await walletStorage.setValue('wallets', toRaw(wallets.value))
      const prevAccount = wallet.value?.accounts[index - 1]
      if (prevAccount) {
        await switchToAccount(prevAccount.index)
      }
    }
  }

  const signPsbt = async (psbtData: string): Promise<string> => {
    if (!psbtData || typeof psbtData !== 'string') {
      throw new Error('Invalid PSBT data: must be a non-empty string')
    }

    // Validate PSBT hex format (basic validation)
    if (!/^[0-9a-fA-F]+$/.test(psbtData)) {
      throw new Error('Invalid PSBT format: must be a valid hex string')
    }

    try {
      let signedPsbt: string

      // Use appropriate signing method based on current network/chain
      if (chain.value === Chain.SATNET) {
        const [error, result] = await walletManager.signPsbt_SatsNet(psbtData, true)
        if (error) {
          throw new Error(`Failed to sign PSBT on SatoshiNet: ${error.message}`)
        }
        signedPsbt = (result as any)?.psbt || (result as any) || ''
      } else {
        const [error, result] = await walletManager.signPsbt(psbtData, true)
        if (error) {
          throw new Error(`Failed to sign PSBT: ${error.message}`)
        }
        signedPsbt = (result as any)?.psbt || (result as any) || ''
      }

      if (!signedPsbt || typeof signedPsbt !== 'string') {
        throw new Error('Invalid response from wallet manager')
      }

      return signedPsbt
    } catch (error: any) {
      console.error('PSBT signing error:', error)
      throw new Error(`PSBT signing failed: ${error.message || 'Unknown error'}`)
    }
  }

  const validateWallet = async (): Promise<boolean> => {
    try {
      if (!address.value || !walletId.value) {
        return false
      }

      // Try to get wallet info to validate wallet state
      await getWalletInfo()

      return address.value.length > 0 && !!walletId.value
    } catch (error) {
      console.error('Wallet validation error:', error)
      return false
    }
  }

  const getFeeRate = (): number => {
    // Return appropriate fee rate based on current chain
    switch (chain.value) {
      case Chain.BTC:
        return btcFeeRate.value
      case Chain.SATNET:
        return satsnetFeeRate.value
      default:
        return feeRate.value
    }
  }

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
    signPsbt,
    validateWallet,
    getFeeRate,
    isSwitchingWallet,
    isSwitchingAccount,
  }
})
