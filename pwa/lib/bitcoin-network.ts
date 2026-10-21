import { bitcoin } from '@/lib/bitcoinjs'
import { Network } from '@/types'

export const bitcoinNetworkFor = (network: string) => {
  if (network === Network.MAINNET) return bitcoin.networks.bitcoin
  if (network === Network.TESTNET) return bitcoin.networks.testnet
  if (network === Network.REGTEST) return bitcoin.networks.regtest
  throw new Error(`Unsupported Bitcoin network: ${network}`)
}
