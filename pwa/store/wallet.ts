import { defineStore } from 'pinia'
import { walletStorage, type AccountRecoveryState } from '@/lib/walletStorage'
import { Network, Chain, WalletData, WalletAccount } from '@/types'
import walletManager, { type MnemonicValidation } from '@/utils/sat20'
import satsnetStp from '@/utils/stp'
import { useChannelStore } from './channel'
import { ref, computed, toRaw } from 'vue'
import { sendNetworkChangedEvent, sendAccountsChangedEvent } from '@/lib/utils'
import { getConfig, logLevel } from '@/config/wasm'
import { awaitAccountChannelRefresh } from '@/lib/accountSwitchChannel'
import {
	isWalletSessionUnlocked,
	isWalletRuntimeUnlocked,
	setWalletSessionUnlocked,
	setWalletRuntimeUnlocked,
	walletRequestSessionGuard,
} from '@/lib/walletSession'
import { withWalletPassword } from '@/lib/walletPasswordPrompt'
import {
	beginWalletIdentityTransition,
	completeWalletIdentityTransition,
	getWalletIdentityState,
	setWalletIdentityPhase,
} from '@/lib/identity-boundary'
import { clearSessionDappGrants } from '@/lib/authorized-origins'
import accountManagementSDK from '@/utils/accountManagement'


export const useWalletStore = defineStore('wallet', () => {
  const channelStore = useChannelStore()
  const address = ref(walletStorage.getValue('address'))
  const publicKey = ref(walletStorage.getValue('pubkey'))
  const walletId = ref(walletStorage.getValue('walletId'))
  const rootAccountId = ref(walletStorage.getValue('rootAccountId'))
  const accountIndex = ref(walletStorage.getValue('accountIndex'))
  const feeRate = ref(0)
  const btcFeeRate = ref(1)
  const satsnetFeeRate = ref(10)
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
      case 'rootAccountId':
        rootAccountId.value = newValue
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

  const setRootAccountId = async (value: string) => {
    await walletStorage.setValue('rootAccountId', value)
    rootAccountId.value = value
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
      fingerprint: item.fingerprint,
      accounts: item.accounts.map(account => ({
        index: account.index,
        name: account.name,
        did: account.did,
        address: account.address,
        pubKey: account.pub_key,
        accountId: account.account_id,
      })),
    }))
  }
  const walletTopologySignature = (catalog: WalletData[]) => JSON.stringify(
    catalog.map(item => ({
      fingerprint: item.fingerprint || '',
      accounts: item.accounts.map(account => Number(account.index)).sort((a, b) => a - b),
    })).sort((a, b) => a.fingerprint.localeCompare(b.fingerprint)),
  )

  const assertWalletTopologyCompatible = (before: WalletData[], after: WalletData[], allowExpansion: boolean) => {
    if (walletTopologySignature(before) === walletTopologySignature(after)) return
    if (!allowExpansion) throw new Error('Wallet topology changed while switching networks')
    for (const sourceWallet of before) {
      if (!sourceWallet.fingerprint) throw new Error('Source wallet is missing its stable fingerprint')
      const targetWallet = after.find(item => item.fingerprint === sourceWallet.fingerprint)
      if (!targetWallet) throw new Error('A source wallet is missing after network recovery')
      const targetAccounts = new Set(targetWallet.accounts.map(account => Number(account.index)))
      if (sourceWallet.accounts.some(account => !targetAccounts.has(Number(account.index)))) {
        throw new Error('A source subaccount is missing after network recovery')
      }
    }
  }

  const walletByRootAccountId = (catalog: WalletData[], candidate?: string) => candidate
    ? catalog.find(item => item.accounts.some(account =>
      Number(account.index) === 0 && account.accountId === candidate))
    : undefined

  const backfillTrustedRootAccountId = async (catalog: WalletData[]) => {
    if (rootAccountId.value) {
      if (!walletByRootAccountId(catalog, rootAccountId.value)) {
        throw new Error('Root account is missing from the local wallet catalog')
      }
      return rootAccountId.value
    }
    const status = await accountManagementSDK.status()
    const managedRoot = status.active && status.account_id
      ? String(status.account_id)
      : ''
    if (managedRoot && !walletByRootAccountId(catalog, managedRoot)) {
      throw new Error('Managed root account is missing from the local wallet catalog')
    }
    if (managedRoot) {
      await setRootAccountId(managedRoot)
      return managedRoot
    }
    return ''
  }
  const setBtcFeeRate = async (value: number) => {
    btcFeeRate.value = value
  }

  const setSatsnetFeeRate = async (value: number) => {
    satsnetFeeRate.value = value
  }

  const openWalletSession = () => {
	setWalletSessionUnlocked(true)
	setWalletRuntimeUnlocked(true)
  }

  const setNetwork = async (value: Network) => {
    if (value === network.value) return true
    if (isSwitchingNetwork.value) return false
    // Confirm before releasing the current manager. The password is local to
    // this switch (including rollback), never retained by the session/store.
    return (await withWalletPassword(password => changeNetwork(value, password))) ?? false
  }

  const recoverTargetNetworkAccount = async (password: string, expectedRootAccountId: string) => {
	const initialCatalog = await readWalletCatalog()
	const rootWallet = walletByRootAccountId(initialCatalog, expectedRootAccountId)
	if (!rootWallet) throw new Error('Target network is missing the root account wallet')
	const [rootWalletErr] = await walletManager.switchWallet(rootWallet.id)
	if (rootWalletErr) throw rootWalletErr
	const [rootAccountErr] = await walletManager.switchAccount(0)
	if (rootAccountErr) throw rootAccountErr
    const deadline = Date.now() + 60_000
    for (;;) {
      const [recoveryErr, recovery] = await walletManager.recoverAccountManagementFromCurrentWallet(
		password,
      )
      if (recoveryErr || !recovery) {
        throw recoveryErr || new Error('Root account discovery failed after network switch')
      }
	  if (!recovery.accountId || recovery.accountId !== expectedRootAccountId) {
		throw new Error('Target network recovery returned a different root account')
	  }
      if (recovery.status !== 'pending') return recovery
      if (Date.now() >= deadline) {
        throw new Error('Target network account synchronization timed out')
      }
      await new Promise(resolve => setTimeout(resolve, 250))
    }
  }

  const changeNetwork = async (value: Network, password: string) => {
    if (value === network.value || isSwitchingNetwork.value) return false
    const sourceCatalog = await readWalletCatalog()
    const fixedRootAccountId = rootAccountId.value || await backfillTrustedRootAccountId(sourceCatalog)
    if (!fixedRootAccountId || !walletByRootAccountId(sourceCatalog, fixedRootAccountId)) {
      throw new Error('Root account is not explicitly available for network switch')
    }
    isSwitchingNetwork.value = true
    const env = 'prd' as const
    const previousNetwork = network.value
    const previousConfig = getConfig(env, previousNetwork)
    const targetConfig = getConfig(env, value)
    const previousWalletId = walletId.value
    const previousAccountIndex = Number(accountIndex.value ?? 0)
    const previousWalletFingerprint = sourceCatalog.find(item => item.id === previousWalletId)?.fingerprint
    if (!previousWalletFingerprint) throw new Error('Current wallet is missing its stable fingerprint')
    const sourceRecoveryWasComplete = accountRecovery.value?.status === 'found'
    let managerTransitioned = false
	const identityGeneration = beginWalletIdentityTransition()

    const restorePreviousManager = async () => {
      const [restoreReleaseErr] = await walletManager.release()
      setWalletRuntimeUnlocked(false)
      if (restoreReleaseErr && !/not initialized/i.test(restoreReleaseErr.message || '')) {
        throw restoreReleaseErr
      }
      const [restoreInitErr] = await walletManager.init(previousConfig, logLevel)
      if (restoreInitErr) throw restoreInitErr
      const [restoreUnlockErr] = await walletManager.unlockWallet(password)
      if (restoreUnlockErr) throw restoreUnlockErr
      setWalletRuntimeUnlocked(true)
      const restoredCatalog = await readWalletCatalog()
      const restoredWallet = restoredCatalog.find(item => item.fingerprint === previousWalletFingerprint)
      if (!restoredWallet) throw new Error('Previous wallet is unavailable after network switch rollback')
      if (restoredWallet.id) {
        const [restoreWalletErr] = await walletManager.switchWallet(
          restoredWallet.id,
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
	  setWalletIdentityPhase(identityGeneration, 'REBUILDING')
      const [releaseErr] = await walletManager.release()
      setWalletRuntimeUnlocked(false)
      if (releaseErr) throw releaseErr
      managerTransitioned = true

      const [initErr] = await walletManager.init(targetConfig, logLevel)
      if (initErr) throw initErr
		const [unlockErr] = await walletManager.unlockWallet(password)
      if (unlockErr) throw unlockErr
      setWalletRuntimeUnlocked(true)

      const recovery = await recoverTargetNetworkAccount(password, fixedRootAccountId)
      const targetRecovery = {
        status: recovery.status,
        code: recovery.code,
        env,
        network: value,
        rootAccountId: fixedRootAccountId,
      }
      const targetCatalog = await readWalletCatalog()
      assertWalletTopologyCompatible(sourceCatalog, targetCatalog, !sourceRecoveryWasComplete)
      if (!walletByRootAccountId(targetCatalog, fixedRootAccountId)) {
        throw new Error('Recovered target catalog does not contain the root account')
      }
      const targetWallet = targetCatalog.find(item => item.fingerprint === previousWalletFingerprint)
      if (!targetWallet) throw new Error('Current wallet is unavailable after target recovery')
      const [selectWalletErr] = await walletManager.switchWallet(targetWallet.id)
      if (selectWalletErr) throw selectWalletErr
      const [selectAccountErr] = await walletManager.switchAccount(previousAccountIndex)
      if (selectAccountErr) throw selectAccountErr

	  setWalletIdentityPhase(identityGeneration, 'VERIFYING')
      const identity = await readWalletIdentity(previousAccountIndex)
      await walletStorage.batchUpdate({
        network: value,
        walletId: targetWallet.id,
        accountIndex: previousAccountIndex,
        address: identity.address,
        pubkey: identity.pubKey,
        accountRecovery: targetRecovery,
		rootAccountId: fixedRootAccountId,
		wallets: toRaw(targetCatalog),
		hasWallet: targetCatalog.length > 0,
      })
      accountRecovery.value = targetRecovery
	  completeWalletIdentityTransition(identityGeneration)

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
		  setWalletIdentityPhase(identityGeneration, 'REBUILDING')
          await restorePreviousManager()
		  setWalletIdentityPhase(identityGeneration, 'VERIFYING')
		  await readWalletIdentity(previousAccountIndex)
		  completeWalletIdentityTransition(identityGeneration)
        } catch (rollbackError) {
          throw new AggregateError([error, rollbackError], 'Network switch and rollback both failed')
        }
	  } else {
		setWalletIdentityPhase(identityGeneration, 'VERIFYING')
		await readWalletIdentity(previousAccountIndex)
		completeWalletIdentityTransition(identityGeneration)
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
    if (value) setWalletSessionUnlocked(false)
    await walletStorage.setValue('locked', value)
    locked.value = value
  }

  const lockWallet = async () => {
	// Invalidate pending UI/DApp work synchronously, before persisting the lock.
	// The SDK wallet and its channel/mining workers continue running.
	beginWalletIdentityTransition()
	setWalletSessionUnlocked(false)
	clearSessionDappGrants()
    locked.value = true
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
	const identityGeneration = beginWalletIdentityTransition()

    try {
      isSwitchingWallet.value = true
	  setWalletIdentityPhase(identityGeneration, 'REBUILDING')

      channelStore.invalidateCurrentChannel()
      const [walletSwitchErr] = await walletManager.switchWallet(
        walletIdToSwitch,
      )
      if (walletSwitchErr) throw walletSwitchErr
      managerWalletSwitched = true

      const [accountSwitchErr] = await walletManager.switchAccount(targetAccountIndex)
      if (accountSwitchErr) throw accountSwitchErr
      managerAccountSwitched = true

	  setWalletIdentityPhase(identityGeneration, 'VERIFYING')
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
	  completeWalletIdentityTransition(identityGeneration)

      try {
        await syncWalletCatalog()
      } catch (catalogError) {
        console.warn('Wallet catalog refresh failed after switch:', catalogError)
      }
      safeSendAccountsChangedEvent(wallets.value)
      refreshCurrentChannelInBackground('wallet switch')

    } catch (error) {
      try {
		setWalletIdentityPhase(identityGeneration, 'REBUILDING')
        if (managerWalletSwitched && previousWalletId && previousWalletId !== walletIdToSwitch) {
          const [restoreWalletErr] = await walletManager.switchWallet(
            previousWalletId,
          )
          if (restoreWalletErr) throw restoreWalletErr
        }
        if (managerAccountSwitched && previousAccountIndex !== targetAccountIndex) {
          const [restoreAccountErr] = await walletManager.switchAccount(previousAccountIndex)
          if (restoreAccountErr) throw restoreAccountErr
        }
		setWalletIdentityPhase(identityGeneration, 'VERIFYING')
		await readWalletIdentity(previousAccountIndex)
		completeWalletIdentityTransition(identityGeneration)
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
    const createsRootWallet = wallets.value.length === 0 && !rootAccountId.value
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
    openWalletSession()
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
    const catalog = await syncWalletCatalog()
    if (createsRootWallet) {
      const created = catalog.find(item => item.id === walletId)
      const createdRootAccountId = created?.accounts.find(account => account.index === 0)?.accountId
      if (!createdRootAccountId) return [new Error('Created root account has no stable public identity'), undefined]
      await setRootAccountId(createdRootAccountId)
    }
    return [undefined, _mnemonic]
  }

  const importWallet = async (
		mnemonic: string,
		password: string,
  ): Promise<[Error | undefined, MnemonicValidation | undefined]> => {
		// The password protects local wallet storage; it is not a BIP39
		// passphrase. Identity derivation must match ImportWallet, which uses an
		// empty BIP39 passphrase.
		const [validationError, mnemonicIdentity] = await walletManager.validateMnemonic(mnemonic, '')
	if (validationError || !mnemonicIdentity) {
	  return [validationError || new Error('Mnemonic validation failed'), undefined]
		}
		const processedMnemonic = mnemonicIdentity.normalized
		let recovered = false
		let res: { walletId: string } | undefined
		let discoveredRecovery: AccountRecoveryState | null = null
		let discoveredRootAccountId = ''
		const shouldDiscoverRoot = !rootAccountId.value || !hasWallet.value || accountRecovery.value?.status === 'pending'
		if (shouldDiscoverRoot) {
		  const [recoveryError, recovery] = await walletManager.recoverAccountManagementFromRootMnemonic(
			processedMnemonic,
			password,
		  )
		  if (recoveryError || !recovery) {
			return [recoveryError || new Error('Root account discovery failed'), undefined]
		  }
		  const env = 'prd'
		  const recoveredRootAccountId = recovery.accountId || mnemonicIdentity.accountId
		  if (!recoveredRootAccountId) {
			return [new Error('Recovered root account has no stable public identity'), undefined]
		  }
		  if (rootAccountId.value && rootAccountId.value !== recoveredRootAccountId) {
			return [new Error('Recovered root account does not match the local root account'), undefined]
		  }
		  const fixedRootAccountId = rootAccountId.value || recoveredRootAccountId
		  discoveredRootAccountId = fixedRootAccountId
		  discoveredRecovery = {
			status: recovery.status,
			code: recovery.code,
			env,
			network: network.value,
			rootAccountId: fixedRootAccountId,
		  }
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
      return [err || new Error('Wallet import failed'), undefined]
    }
    const { walletId } = res
		if (discoveredRecovery) {
		  await walletStorage.batchUpdate({
			accountRecovery: discoveredRecovery,
			rootAccountId: discoveredRootAccountId,
		  })
		  accountRecovery.value = discoveredRecovery
		  rootAccountId.value = discoveredRootAccountId
		}
		if (accountRecovery.value &&
		  accountRecovery.value.env === 'prd' &&
		  accountRecovery.value.network === network.value &&
		  !accountRecovery.value.rootAccountId && rootAccountId.value) {
		  accountRecovery.value = { ...accountRecovery.value, rootAccountId: rootAccountId.value }
		  await walletStorage.setValue('accountRecovery', accountRecovery.value)
		}
    await setWalletId(walletId)
    await setAccountIndex(0)
    await setHasWallet(true)
    await setLocked(false)
    // await setNetwork(Network.TESTNET)
    await setChain(Chain.BTC)
    openWalletSession()
    refreshCurrentChannelInBackground('importWallet')
    const [_e, addressRes] = await walletManager.getWalletAddress(
      accountIndex.value
    )
    const [_j, pubkeyRes] = await walletManager.getWalletPubkey(
      accountIndex.value
    )
		const _wallets = recovered
		  ? await readWalletCatalog()
		  : structuredClone(walletStorage.getValue('wallets'))
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
		const catalog = await syncWalletCatalog()
		if (!rootAccountId.value) {
		  const imported = catalog.find(item => item.id === walletId)
		  const importedRootAccountId = imported?.accounts.find(account => account.index === 0)?.accountId
		  if (!importedRootAccountId) return [new Error('Imported root account has no stable public identity'), undefined]
		  await setRootAccountId(importedRootAccountId)
		}
	return [undefined, mnemonicIdentity]
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
    const runtimeRunning = isWalletRuntimeUnlocked()
    const checkSession = walletRequestSessionGuard('unlockWallet')
    const [err, result] = await walletManager.unlockWallet(password)

    if (!err && result) {
      try {
        if (!runtimeRunning) {
          await getWalletInfo()
          const catalog = await syncWalletCatalog()
          await backfillTrustedRootAccountId(catalog)
		} else if (!rootAccountId.value) {
		  await backfillTrustedRootAccountId(wallets.value)
        }
        checkSession()
        await setLocked(false)
        checkSession()
        openWalletSession()
        checkSession()
        // UI unlock must not switch/rebuild a live channel with pending work.
        if (runtimeRunning) {
          completeWalletIdentityTransition(getWalletIdentityState().generation)
        } else {
          await switchToAccount(accountIndex.value)
        }
        refreshCurrentChannelInBackground('unlockWallet')
        return [undefined, result]
      } catch (error) {
        // A lock may have happened while the unlocked flag was being stored.
        // Do not leave that late write showing an unauthenticated UI as open.
        if (!isWalletSessionUnlocked()) await setLocked(true)
        return [error, undefined]
      }
    }

    return [err, result]
  }

  const deleteWallet = async (walletIdToDelete: string) => {
    try {
		  const walletToDelete = wallets.value.find(item => item.id === walletIdToDelete)
		  if (walletToDelete?.accounts.some(account => account.index === 0 && account.accountId === rootAccountId.value)) {
			return [new Error('Root wallet cannot be deleted'), undefined]
	  }
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
		await switchWallet(nextWallet.id)
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
	await switchToAccount(accountId)
	if (address.value && publicKey.value) {
      const newAccount: WalletAccount = {
        index: accountId,
        name,
		address: address.value,
		pubKey: publicKey.value,
      }
      const _wallets = structuredClone(walletStorage.getValue('wallets'))
      const _wallet = _wallets?.find((w: any) => w.id === walletId.value)
      if (_wallet) {
        _wallet.accounts.push(newAccount)
      }
      wallets.value = _wallets
      await walletStorage.setValue('wallets', _wallets)
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
	const identityGeneration = beginWalletIdentityTransition()

    try {
      isSwitchingAccount.value = true
	  setWalletIdentityPhase(identityGeneration, 'REBUILDING')

      channelStore.invalidateCurrentChannel()
      const [accountSwitchErr] = await walletManager.switchAccount(accountId)
      if (accountSwitchErr) throw accountSwitchErr
      managerAccountSwitched = true
	  setWalletIdentityPhase(identityGeneration, 'VERIFYING')
      const identity = await readWalletIdentity(accountId)
      await walletStorage.batchUpdate({
        accountIndex: accountId,
        address: identity.address,
        pubkey: identity.pubKey,
      })
      accountIndex.value = accountId
      address.value = identity.address
      publicKey.value = identity.pubKey
	  completeWalletIdentityTransition(identityGeneration)

      // The SDK lookup is local, but waiting here keeps the account identity
      // and channel view on the same request generation. Failure is contained
      // because the account switch has already succeeded and must not roll back.
      await awaitAccountChannelRefresh(
        () => channelStore.getCurrentChannel(),
        (channelError) => console.warn('Channel refresh failed during account switch:', channelError),
      )

    } catch (error) {
	  try {
		setWalletIdentityPhase(identityGeneration, 'REBUILDING')
		if (managerAccountSwitched && previousAccountIndex !== accountId) {
          const [restoreAccountErr] = await walletManager.switchAccount(previousAccountIndex)
          if (restoreAccountErr) throw restoreAccountErr
		}
		setWalletIdentityPhase(identityGeneration, 'VERIFYING')
		await readWalletIdentity(previousAccountIndex)
		completeWalletIdentityTransition(identityGeneration)
		channelStore.invalidateCurrentChannel()
		refreshCurrentChannelInBackground('account switch rollback')
	  } catch (restoreError) {
		console.warn('Failed to restore account after switch failure:', restoreError)
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
    rootAccountId,
    setRootAccountId,
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
