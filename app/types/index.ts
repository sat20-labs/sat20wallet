export enum Network {
  LIVENET = 'livenet',
  TESTNET = 'testnet',
  // REGTEST = 'regtest',
  // TESTNET4 = 'testnet4',
}
export type Env = 'dev' | 'test' | 'prd';
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
  id: string
  name: string
  avatar?: string
  accounts: WalletAccount[]
}

// 导出节点质押相关类型
export type { NodeStakeData } from './nodeStake'

// --- UTXO Management for Ordinals ---
export interface FailedUtxoInfo {
  utxo: string
  reason: string
}

export interface UnlockOrdinalsResp {
  failedUtxos: FailedUtxoInfo[]
}

export interface LockedUtxoInfo {
  utxo: string
  txid: string
  vout: string
  reason?: string
  lockedTime?: number
}

// API Response types for locked UTXOs
export interface AssetName {
  Protocol: string
  Type: string
  Ticker: string
}

export interface AssetOffset {
  Start: number
  End: number
}

export interface Asset {
  Name: AssetName
  Amount: string
  Precision: number
  BindingSat: number
  Offsets: AssetOffset[]
}

export interface LockedUtxoApiResponse {
  UtxoId: number
  Outpoint: string
  Value: number
  PkScript: string
  Assets: Asset[]
}

export interface LockedUtxosApiResponse {
  code: number
  msg: string
  data: LockedUtxoApiResponse[]
}
