import { defineStore } from 'pinia'
import { walletStorage } from '@/lib/walletStorage'
import { Network, Chain, WalletData, WalletAccount } from '@/types'
import { Message } from '@/types/message'
import walletManager from '@/utils/sat20'
import stp from '@/utils/stp'
import satsnetStp from '@/utils/stp'
import { useChannelStore } from './channel'
import { ref, computed, toRaw } from 'vue'
import { sendNetworkChangedEvent } from '@/lib/utils'


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
  console.log(wallets);
  console.log(walletId);
  const wallet = computed(() => wallets.value.find(w => w.id === walletId.value))
  const accounts = computed(() => wallet.value?.accounts)
  const account = computed(() => wallet.value?.accounts.find(a => a.index === accountIndex.value))
  const setAddress = async (value: string) => {
    console.log('old address', address.value);

    address.value = value
    console.log('setAddress', value)
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
    // const [err] = await walletManager.switchChain(n, password.value as string)
    // console.log(err);

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
      sendNetworkChangedEvent(value)
    } catch (error) {
      console.error('Failed to send NETWORK_CHANGED message to background:', error)
    }
    await stp.release()
    await walletManager.release()
    window.location.reload()
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
  const switchWallet = async (walletId: string) => {
    await walletManager.switchWallet(walletId, password.value as string)
    await satsnetStp.switchWallet(walletId, password.value as string)
    const currentAccount = wallets.value.find(w => w.id === walletId)?.accounts[0];
    await setWalletId(walletId);
    await switchToAccount(currentAccount?.index || 0);
    await getWalletInfo()
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
    // await setNetwork(Network.TESTNET)
    await setChain(Chain.BTC)
    // await getWalletInfo()
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
    const [err, res] = await walletManager.importWallet(mnemonic?.trim(), password)
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
    await satsnetStp.importWallet(mnemonic, password)
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

    return [undefined, mnemonic]
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
      await switchToAccount(accountIndex.value)
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
      const _wallets = JSON.parse(JSON.stringify(walletStorage.getValue('wallets')))
      const _wallet = _wallets.find((w: any) => w.id === walletId.value)
      _wallet.accounts.push(newAccount)
      wallets.value = _wallets
      await walletStorage.setValue('wallets', _wallets)
      await setAccountIndex(accountId)
      console.log(addressRes.address);

      await setAddress(addressRes.address)
      console.log('importWallet', _wallets);
      console.log('wallet id', walletId);
      console.log('wallet id', await walletStorage.getValue('walletId'));

      await setPublickey(pubkeyRes.pubKey)
    }
  }

  const switchToAccount = async (accountId: number) => {
    await walletManager.switchAccount(accountId)
    await satsnetStp.switchAccount(accountId)
    const [_, addressRes] = await walletManager.getWalletAddress(accountId)
    const [__, pubkeyRes] = await walletManager.getWalletPubkey(accountId)
    console.log('switchToAccount', await satsnetStp.getWallet());
    if (addressRes && pubkeyRes) {
      await setAccountIndex(accountId)
      await setAddress(addressRes.address)
      await setPublickey(pubkeyRes.pubKey)
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
    deleteAccount,
    accounts,
    switchWallet,
    btcFeeRate,
    setBtcFeeRate,
    satsnetFeeRate,
    setSatsnetFeeRate,
  }
})
