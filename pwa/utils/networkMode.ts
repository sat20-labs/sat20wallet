import { Network } from '@/types'

export const isRegtestBuild = import.meta.env.MODE === 'regtest'
export const defaultWalletNetwork = isRegtestBuild ? Network.REGTEST : Network.MAINNET
export const defaultWalletEnv: 'dev' | 'prd' = isRegtestBuild ? 'dev' : 'prd'
