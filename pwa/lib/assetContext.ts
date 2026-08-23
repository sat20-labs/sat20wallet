export interface AssetContext {
  env: string
  network: string
  chain: string
  walletId: string
  accountIndex: number
  address: string
}

export const assetContextKey = ({
  env,
  network,
  chain,
  walletId,
  accountIndex,
  address,
}: AssetContext) => [env, network, chain, walletId, accountIndex, address].join(':')

export const isSameAssetContext = (left: AssetContext | null, right: AssetContext | null) => (
  !!left && !!right && assetContextKey(left) === assetContextKey(right)
)
