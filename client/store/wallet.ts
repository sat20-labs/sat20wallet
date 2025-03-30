import { defineStore } from 'pinia'
import { walletStorage } from '@/lib/walletStorage'
import { Network, Chain } from '@/types'
import walletManager from '@/utils/sat20'
import satsnetStp from '@/utils/stp'
import { useChannelStore } from './channel'
import { useL1Store } from './l1'
import { ref } from 'vue'

export const useWalletStore = defineStore('wallet', () => {
  const channelStore = useChannelStore()
  
  const address = ref(walletStorage.getValue('address'))
  const publicKey = ref(walletStorage.getValue('pubkey'))
  const walletId = ref(walletStorage.getValue('walletId'))
  const accountIndex = ref(walletStorage.getValue('accountIndex'))
  const feeRate = ref(0)
  const password = ref(walletStorage.getValue('password'))
  const network = ref(walletStorage.getValue('network'))
  const chain = ref(walletStorage.getValue('chain'))
  const locked = ref(true)
  const hasWallet = ref(walletStorage.getValue('hasWallet'))

  const setAddress = async (value: string) => {
    await walletStorage.setValue('address', value)
    address.value = value
  }

  const setWalletId = async (value: number) => {
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

  const setPassword = async (value: string) => {
    await walletStorage.updatePassword(value)
    password.value = value
  }

  const setNetwork = async (value: Network) => {
    const n = value === Network.LIVENET ? 'mainnet' : 'testnet'

    const [err] = await walletManager.switchChain(n)
    if (!err) {
      await walletStorage.setValue('network', value)
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
      const { walletId } = result
      await setWalletId(walletId)
      await getWalletInfo() // This sets the public key
      await setLocked(false)
      await setPassword(password)
      await satsnetStp.unlockWallet(password)
      // Start STP after wallet info and public key are set
      await satsnetStp.start()
      await channelStore.getAllChannels()
    }
    return [err, result]
  }

  const deleteWallet = async () => {
    try {
      await walletStorage.clear()
      hasWallet.value = false
      address.value = ''
      publicKey.value = ''
      walletId.value = 0
      return [undefined, true]
    } catch (error) {
      console.error('Failed to delete wallet:', error)
      return [error, undefined]
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
  }
})
