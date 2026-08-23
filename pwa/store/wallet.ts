import { defineStore } from 'pinia'
import { walletStorage, type AccountRecoveryState } from '@/lib/walletStorage'
import { Network, Chain, WalletData, WalletAccount } from '@/types'
import walletManager from '@/utils/sat20'
import satsnetStp from '@/utils/stp'
import { useChannelStore } from './channel'
import { ref, computed, toRaw } from 'vue'
import { sendNetworkChangedEvent, sendAccountsChangedEvent } from '@/lib/utils'
import { getConfig, logLevel } from '@/config/wasm'
import { awaitAccountChannelRefresh } from '@/lib/accountSwitchChannel'


export const useWalletStore = defineStore('wallet', () => {
  const channelStore = useChannelStore()
  const address = ref(walletStorage.getValue('address'))
  const publicKey = ref(walletStorage.getValue('pubkey'))
  const walletId = ref(walletStorage.getValue('walletId'))
  const accountIndex = ref(walletStorage.getValue('accountIndex'))
  const feeRate = ref(0)
  const btcFeeRate = ref(1)
  const satsnetFeeRate = ref(10)
  const password = ref('')
  const network = ref(walletStorage.getValue('network'))
  const chain = ref(walletStorage.getValue('chain'))
  const locked = ref(walletStorage.getValue('locked') ?? true)
  const hasWallet = ref(!!walletStorage.getValue('hasWallet'))
  const localWallets = walletStorage.getValue('wallets');
  const wallets = ref<WalletData[]>(localWallets ? structuredClone(localWallets) : [])
  const accountRecovery = ref<AccountRecoveryState | null>(walletStorage.getValue('accountRecovery'))

  // 添加全局切换状态管理
  const isSwitchingWallet = ref(false)
  const isSwitchingAccount = ref(false)
  const isSwitchingNetwork = ref(false)

  // 监听 walletStorage 状态变化，同步到 walletStore
  walletStorage.subscribe((key, newValue, oldValue) => {
    switch (key) {
      case 'locked':
        locked.value = newValue ?? true
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

  const refreshCurrentChannelInBackground = (label: string) => {
    void channelStore.getCurrentChannel().catch((error) => {
      console.warn(`Channel refresh failed during ${label}:`, error)
    })
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

  const readWalletIdentity = async (index: number) => {
    const [addressErr, addressRes] = await walletManager.getWalletAddress(index)
    if (addressErr || !addressRes?.address) {
      throw addressErr || new Error('Failed to load wallet address')
    }
    const [pubkeyErr, pubkeyRes] = await walletManager.getWalletPubkey(index)
    if (pubkeyErr || !pubkeyRes?.pubKey) {
      throw pubkeyErr || new Error('Failed to load wallet public key')
    }
    return {
      address: addressRes.address,
      pubKey: pubkeyRes.pubKey,
    }
  }

  const readWalletCatalog = async (): Promise<WalletData[]> => {
    const [err, result] = await walletManager.getWalletCatalog()
    if (err || !result) {
      throw err || new Error('Failed to load wallet catalog')
    }
    return result.wallets.map(item => ({
      id: String(item.id),
      name: item.name,
      accounts: item.accounts.map(account => ({
        index: account.index,
        name: account.name,
        did: account.did,
        address: account.address,
        pubKey: account.pub_key,
      })),
    }))
  }
  const setBtcFeeRate = async (value: number) => {
    btcFeeRate.value = value
  }

  const setSatsnetFeeRate = async (value: number) => {
    satsnetFeeRate.value = value
  }

  const setPassword = async (value: string) => {
    password.value = value
  }

  const setNetwork = async (value: Network) => {
    if (value === network.value) return true
    if (isSwitchingNetwork.value) return false
    isSwitchingNetwork.value = true
    const env = walletStorage.getValue('env') || 'test'
    const previousNetwork = network.value
    const previousConfig = getConfig(env, previousNetwork)
    const targetConfig = getConfig(env, value)
    const previousWalletId = walletId.value
    const previousAccountIndex = Number(accountIndex.value ?? 0)
    const previousRecovery = accountRecovery.value
      ? structuredClone(accountRecovery.value)
      : null
    let managerTransitioned = false

    const restorePreviousManager = async () => {
      const [restoreReleaseErr] = await walletManager.release()
      if (restoreReleaseErr && !/not initialized/i.test(restoreReleaseErr.message || '')) {
        throw restoreReleaseErr
      }
      const [restoreInitErr] = await walletManager.init(previousConfig, logLevel)
      if (restoreInitErr) throw restoreInitErr
      const [restoreUnlockErr] = await walletManager.unlockWallet(password.value as string)
      if (restoreUnlockErr) throw restoreUnlockErr
      if (previousWalletId) {
        const [restoreWalletErr] = await walletManager.switchWallet(
          previousWalletId,
          password.value as string,
        )
        if (restoreWalletErr) throw restoreWalletErr
      }
      const [restoreAccountErr] = await walletManager.switchAccount(previousAccountIndex)
      if (restoreAccountErr) throw restoreAccountErr
      channelStore.invalidateCurrentChannel()
      refreshCurrentChannelInBackground('network switch rollback')
    }

    try {
      channelStore.invalidateCurrentChannel()
      const [releaseErr] = await walletManager.release()
      if (releaseErr) throw releaseErr
      managerTransitioned = true

      const [initErr] = await walletManager.init(targetConfig, logLevel)
      if (initErr) throw initErr
      const [unlockErr] = await walletManager.unlockWallet(password.value as string)
      if (unlockErr) throw unlockErr

      if (previousWalletId) {
        const [selectWalletErr] = await walletManager.switchWallet(
          previousWalletId,
          password.value as string,
        )
        if (selectWalletErr) throw selectWalletErr
      }
      const [selectAccountErr] = await walletManager.switchAccount(previousAccountIndex)
      if (selectAccountErr) throw selectAccountErr

      let targetWalletId = previousWalletId
      let targetAccountIndex = previousAccountIndex
      let targetRecovery = previousRecovery
      let targetCatalog: WalletData[] | undefined
      const shouldRetryAccountRecovery = !!previousRecovery?.rootWalletId &&
        previousRecovery.rootWalletId === previousWalletId &&
        (previousRecovery.env !== env || previousRecovery.network !== value || previousRecovery.status === 'pending')
      if (shouldRetryAccountRecovery) {
        const [recoveryErr, recovery] = await walletManager.recoverAccountManagementFromCurrentWallet(
          password.value as string,
        )
        if (recoveryErr || !recovery) {
          throw recoveryErr || new Error('Root account discovery failed after network switch')
        }
        const recoveredWalletId = recovery.walletId || previousWalletId
        targetRecovery = {
          status: recovery.status,
          code: recovery.code,
          env,
          network: value,
          rootWalletId: recoveredWalletId,
        }
        if (recovery.status === 'found') {
          targetWalletId = recoveredWalletId
          targetAccountIndex = 0
          targetCatalog = await readWalletCatalog()
        }
      }

      const identity = await readWalletIdentity(targetAccountIndex)
      await walletStorage.batchUpdate({
        network: value,
        walletId: targetWalletId,
        accountIndex: targetAccountIndex,
        address: identity.address,
        pubkey: identity.pubKey,
        accountRecovery: targetRecovery,
        ...(targetCatalog
          ? { wallets: toRaw(targetCatalog), hasWallet: targetCatalog.length > 0 }
          : {}),
      })
      accountRecovery.value = targetRecovery

      refreshCurrentChannelInBackground('network switch')

      try {
        console.log(`Sending NETWORK_CHANGED message with payload: ${value}`)
        await sendNetworkChangedEvent(value)
      } catch (error) {
        console.error('Failed to send NETWORK_CHANGED message to background:', error)
      }
      return true
    } catch (error) {
      if (managerTransitioned) {
        try {
          await restorePreviousManager()
        } catch (rollbackError) {
          throw new AggregateError([error, rollbackError], 'Network switch and rollback both failed')
        }
      }
      throw error
    } finally {
      isSwitchingNetwork.value = false
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

  const lockWallet = async () => {
    await setPassword('')
    await setLocked(true)
  }

  const setHasWallet = async (value: boolean) => {
    await walletStorage.setValue('hasWallet', value)
    hasWallet.value = value
  }

  const setFeeRate = (value: number) => {
    feeRate.value = value
  }

  const syncWalletCatalog = async () => {
    const catalog = await readWalletCatalog()
    wallets.value = catalog
    await walletStorage.setValue('wallets', toRaw(catalog))
    await setHasWallet(catalog.length > 0)
    return catalog
  }
  const switchWallet = async (walletIdToSwitch: string) => {
    // 如果正在切换，直接返回
    if (isSwitchingWallet.value || isSwitchingAccount.value) {
      console.log('Wallet switch already in progress, ignoring...')
      return
    }

    const targetWallet = wallets.value.find(w => w.id === walletIdToSwitch)
    const targetAccountIndex = targetWallet?.accounts[0]?.index
    if (!targetWallet || targetAccountIndex === undefined) {
      throw new Error(`Wallet ${walletIdToSwitch} is not available`)
    }
    const previousWalletId = walletId.value
    const previousAccountIndex = Number(accountIndex.value ?? 0)
    let managerWalletSwitched = false
    let managerAccountSwitched = false

    try {
      isSwitchingWallet.value = true
      console.log('Starting wallet switch to:', walletIdToSwitch)

      channelStore.invalidateCurrentChannel()
      const [walletSwitchErr] = await walletManager.switchWallet(
        walletIdToSwitch,
        password.value as string,
      )
      if (walletSwitchErr) throw walletSwitchErr
      managerWalletSwitched = true

      const [accountSwitchErr] = await walletManager.switchAccount(targetAccountIndex)
      if (accountSwitchErr) throw accountSwitchErr
      managerAccountSwitched = true

      const identity = await readWalletIdentity(targetAccountIndex)
      await walletStorage.batchUpdate({
        walletId: walletIdToSwitch,
        accountIndex: targetAccountIndex,
        address: identity.address,
        pubkey: identity.pubKey,
      })
      walletId.value = walletIdToSwitch
      accountIndex.value = targetAccountIndex
      address.value = identity.address
      publicKey.value = identity.pubKey

      try {
        await syncWalletCatalog()
      } catch (catalogError) {
        console.warn('Wallet catalog refresh failed after switch:', catalogError)
      }
      safeSendAccountsChangedEvent(wallets.value)
      refreshCurrentChannelInBackground('wallet switch')

      console.log('Wallet switch completed successfully')
    } catch (error) {
      console.error('Wallet switch failed:', error)
      try {
        if (managerWalletSwitched && previousWalletId && previousWalletId !== walletIdToSwitch) {
          const [restoreWalletErr] = await walletManager.switchWallet(
            previousWalletId,
            password.value as string,
          )
          if (restoreWalletErr) throw restoreWalletErr
        }
        if (managerAccountSwitched && previousAccountIndex !== targetAccountIndex) {
          const [restoreAccountErr] = await walletManager.switchAccount(previousAccountIndex)
          if (restoreAccountErr) throw restoreAccountErr
        }
        channelStore.invalidateCurrentChannel()
        refreshCurrentChannelInBackground('wallet switch rollback')
      } catch (restoreError) {
        console.warn('Failed to restore wallet manager after switch failure:', restoreError)
      }
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
    refreshCurrentChannelInBackground('createWallet')
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
    await syncWalletCatalog()
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

    let recovered = false
    let res: { walletId: string } | undefined
    const shouldDiscoverRoot = !hasWallet.value || accountRecovery.value?.status === 'pending'
    if (shouldDiscoverRoot) {
      const [recoveryErr, recovery] = await walletManager.recoverAccountManagementFromRootMnemonic(
        processedMnemonic,
        password
      )
      if (recoveryErr || !recovery) {
        return [recoveryErr || new Error('Root account discovery failed'), undefined]
      }
      const env = walletStorage.getValue('env') || 'test'
      accountRecovery.value = {
        status: recovery.status,
        code: recovery.code,
        env,
        network: network.value,
        rootWalletId: recovery.walletId,
      }
      await walletStorage.setValue('accountRecovery', accountRecovery.value)
      if (recovery.status === 'found') {
        if (!recovery.walletId) {
          return [new Error('Recovered account has no current wallet'), undefined]
        }
        recovered = true
        res = { walletId: recovery.walletId }
      }
    }

    let err: Error | undefined
    if (!recovered) {
      ;[err, res] = await walletManager.importWallet(processedMnemonic, password)
    }
    if (err || !res) {
      console.error(err)
      return [err, undefined]
    }
    const { walletId } = res
    if (accountRecovery.value &&
      accountRecovery.value.env === (walletStorage.getValue('env') || 'test') &&
      accountRecovery.value.network === network.value &&
      !accountRecovery.value.rootWalletId) {
      accountRecovery.value = { ...accountRecovery.value, rootWalletId: walletId }
      await walletStorage.setValue('accountRecovery', accountRecovery.value)
    }
    await setWalletId(walletId)
    await setAccountIndex(0)
    await setHasWallet(true)
    await setLocked(false)
    // await setNetwork(Network.TESTNET)
    await setChain(Chain.BTC)
    await setPassword(password)
    refreshCurrentChannelInBackground('importWallet')
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
      if (!recovered) {
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
    }
    wallets.value = _wallets
    await walletStorage.setValue('wallets', _wallets)
    await syncWalletCatalog()
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
      await syncWalletCatalog()
      await switchToAccount(accountIndex.value)
      refreshCurrentChannelInBackground('unlockWallet')
      return [undefined, result]
    } else if (isAlreadyUnlocked) {
      // 钱包已经解锁，但前端状态可能是锁定的，需要同步状态
      console.log('检测到钱包已解锁，同步前端状态')
      await getWalletInfo()
      await setLocked(false)
      await setPassword(password)
      await syncWalletCatalog()
      await switchToAccount(accountIndex.value)
      refreshCurrentChannelInBackground('unlockWallet existing')
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
      const [deleteError] = await walletManager.deleteWallet(walletIdToDelete)
      if (deleteError) {
        return [deleteError, undefined]
      }
      const catalog = await syncWalletCatalog()
      if (isDeletingActiveWallet) {
        const nextWalletIndex = walletIndexToDelete > 0 ? walletIndexToDelete - 1 : 0
        const nextWallet = catalog[nextWalletIndex] || catalog[0]
        if (!nextWallet || nextWallet.accounts.length === 0) {
          return [new Error('No wallet is available after deletion'), undefined]
        }
        await setWalletId(nextWallet.id)
        await walletManager.switchWallet(nextWallet.id, password.value)
        await switchToAccount(0)
        await getWalletInfo()
      }

      return [undefined, true];
    } catch (error: any) {
      console.error('Failed to delete wallet:', error);
      return [error, undefined];
    }
  }

  const addAccount = async (name: string, accountId: number) => {
    const [ensureError] = await walletManager.ensureAccount(walletId.value, accountId, name)
    if (ensureError) throw ensureError
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
    await syncWalletCatalog()
  }

  const switchToAccount = async (accountId: number) => {
    // 如果正在切换账户，直接返回
    if (isSwitchingAccount.value || isSwitchingWallet.value) {
      console.log('Account switch already in progress, ignoring...')
      return
    }

    const previousAccountIndex = Number(accountIndex.value ?? 0)
    let managerAccountSwitched = false

    try {
      isSwitchingAccount.value = true
      console.log('Starting account switch to:', accountId)

      channelStore.invalidateCurrentChannel()
      const [accountSwitchErr] = await walletManager.switchAccount(accountId)
      if (accountSwitchErr) throw accountSwitchErr
      managerAccountSwitched = true
      const identity = await readWalletIdentity(accountId)
      await walletStorage.batchUpdate({
        accountIndex: accountId,
        address: identity.address,
        pubkey: identity.pubKey,
      })
      accountIndex.value = accountId
      address.value = identity.address
      publicKey.value = identity.pubKey

      // The SDK lookup is local, but waiting here keeps the account identity
      // and channel view on the same request generation. Failure is contained
      // because the account switch has already succeeded and must not roll back.
      await awaitAccountChannelRefresh(
        () => channelStore.getCurrentChannel(),
        (channelError) => console.warn('Channel refresh failed during account switch:', channelError),
      )

      console.log('Account switch completed successfully')
    } catch (error) {
      console.error('Account switch failed:', error)
      if (managerAccountSwitched && previousAccountIndex !== accountId) {
        try {
          const [restoreAccountErr] = await walletManager.switchAccount(previousAccountIndex)
          if (restoreAccountErr) throw restoreAccountErr
          channelStore.invalidateCurrentChannel()
          refreshCurrentChannelInBackground('account switch rollback')
        } catch (restoreError) {
          console.warn('Failed to restore account after switch failure:', restoreError)
        }
      }
      throw error
    } finally {
      isSwitchingAccount.value = false
    }
  }

  const updateAccountName = async (accountId: number, newName: string) => {
    const current = wallet.value?.accounts.find(a => a.index === accountId)
    const [err] = await walletManager.updateAccountMetadata(
      walletId.value, accountId, newName, current?.did || ''
    )
    if (err) throw err
    await syncWalletCatalog()
  }

  const updateWalletName = async (walletId: string, newName: string) => {
    const [err] = await walletManager.updateWalletName(walletId, newName)
    if (err) throw err
    await syncWalletCatalog()
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
    lockWallet,
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
    accountRecovery,
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
    syncWalletCatalog,
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
    isSwitchingNetwork,
  }
})
