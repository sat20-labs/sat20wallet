export enum Network {
  LIVENET = 'livenet',
  TESTNET = 'testnet',
  // REGTEST = 'regtest',
  // TESTNET4 = 'testnet4',
}

export enum Chain {
  BTC = 'btc',
  SATNET = 'satnet',
}

export type Balance = { confirmed: number; unconfirmed: number; total: number };

export interface WalletAccount {
  index: number
  name: string
  address: string
  pubKey: string
}

export interface WalletData {
  id: number
  name: string
  avatar?: string
  accounts: WalletAccount[]
}
