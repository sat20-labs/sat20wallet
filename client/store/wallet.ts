import { defineStore } from 'pinia'
import { walletStorage } from '@/lib/walletStorage'
import { Network } from '@/types'
import walletManager from '@/utils/sat20'
export const useWalletStore = defineStore('wallet', () => {
  const address = ref(walletStorage.address)
  const publicKey = ref(walletStorage.pubkey)
  const walletId = ref(walletStorage.walletId)
  const accountIndex = ref(walletStorage.accountIndex)
  const feeRate = ref(0)
  const network = ref(walletStorage.network)
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
    await getWalletInfo()
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
    await getWalletInfo()
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
  return {
    address,
    setAddress,
    walletId,
    setWalletId,
    network,
    setNetwork,
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
  }
})
