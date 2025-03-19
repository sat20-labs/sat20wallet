import { defineStore } from 'pinia'
import { walletStorage } from '@/lib/walletStorage'
import { Network, Chain } from '@/types'
import walletManager from '@/utils/sat20'
import satsnetStp from '@/utils/stp'
import { useChannelStore } from './channel'

export const useWalletStore = defineStore('wallet', () => {
  const channelStore = useChannelStore()
  const address = ref(walletStorage.address)
  const publicKey = ref(walletStorage.pubkey)
  const walletId = ref(walletStorage.walletId)
  const accountIndex = ref(walletStorage.accountIndex)
  const feeRate = ref(0)
  const password = ref(walletStorage.password)
  const network = ref(walletStorage.network)
  const chain = ref(walletStorage.chain)
  const locked = ref(walletStorage.locked)
  const hasWallet = ref(walletStorage.hasWallet)

  const setAddress = (value: string) => {
    walletStorage.address = value as any
    address.value = value
  }

  const setWalletId = (value: number) => {
    walletStorage.walletId = value
    walletId.value = value
  }

  const setAccountIndex = (value: number) => {
    walletStorage.accountIndex = value
    accountIndex.value
  }
  const setPublickey = (value: string) => {
    walletStorage.pubkey = value
    publicKey.value = value
  }
  const setPassword = (value: string) => {
    walletStorage.password = value
    password.value = value
  }
  const setNetwork = async (value: Network) => {
    const n = value === Network.LIVENET ? 'mainnet' : 'testnet'

    const [err] = await walletManager.switchChain(n)
    if (!err) {
      walletStorage.network = value
      network.value = value
      const [_, addressRes] = await walletManager.getWalletAddress(0)
      const [__, pubkeyRes] = await walletManager.getWalletPubkey(0)

      if (addressRes && pubkeyRes) {
        const { address } = addressRes
        await setAddress(address)
        await setPublickey(pubkeyRes.pubKey)
      }
    }
  }

  const setChain = async (value: Chain) => {
    walletStorage.chain = value
    chain.value = value
  }

  const setLocked = (value: boolean) => {
    // walletStorage.locked = value
    locked.value = value
  }

  const setHasWallet = (value: boolean) => {
    hasWallet.value = value
    walletStorage.hasWallet = value
  }

  const setFeeRate = (value: number) => {
    feeRate.value = value
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
    await setNetwork(Network.TESTNET)
    await setChain(Chain.BTC)
    await getWalletInfo()
    await satsnetStp.importWallet(_mnemonic, password)
    await setPassword(password)
    await satsnetStp.start()
    await channelStore.getAllChannels()
    return [undefined, _mnemonic]
  }
  const importWallet = async (mnemonic: string, password: string) => {
    const [err, res] = await walletManager.importWallet(mnemonic, password)
    if (err || !res) {
      console.error(err)
      return [err, undefined]
    }
    const { walletId } = res
    await setWalletId(walletId)
    await setAccountIndex(0)
    await setHasWallet(true)
    await setLocked(false)
    await setNetwork(Network.TESTNET)
    await setChain(Chain.BTC)
    await getWalletInfo()
    await setPassword(password)
    await satsnetStp.importWallet(mnemonic, password)
    await satsnetStp.start()
    await channelStore.getAllChannels()
    return [undefined, mnemonic]
  }
  const getWalletInfo = async () => {
    const [_e, addressRes] = await walletManager.getWalletAddress(
      accountIndex.value
    )
    const [_j, pubkeyRes] = await walletManager.getWalletPubkey(
      accountIndex.value
    )
    // const [_c, chainRes] = await walletManager.getChain()
    // console.log(chainRes);

    if (addressRes && pubkeyRes) {
      const { address } = addressRes
      await setAddress(address)
      await setPublickey(pubkeyRes.pubKey)
    }
  }
  const unlockWallet = async (password: string) => {
    const [err, result] = await walletManager.unlockWallet(password)
    if (!err && result) {
      const { walletId } = result
      setWalletId(walletId)
      await getWalletInfo()
      await satsnetStp.unlockWallet(password)
      await satsnetStp.start()
      await channelStore.getAllChannels()
      await setLocked(false)
      await setPassword(password)
    }
    return [err, result]
  }
  const deleteWallet = () => {
    walletStorage.clear()
    hasWallet.value = false
    address.value = ''
    publicKey.value = ''
    walletId.value = 0
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
  }
})
