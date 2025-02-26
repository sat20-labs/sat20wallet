export enum Network {
  LIVENET = 'livenet',
  TESTNET = 'testnet',
  // REGTEST = 'regtest',
  // TESTNET4 = 'testnet4',
}

export type Balance = { confirmed: number; unconfirmed: number; total: number };