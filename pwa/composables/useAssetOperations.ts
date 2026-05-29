import { useRouter } from 'vue-router'

interface Asset {
  id: string
  ticker?: string
  label?: string
  amount?: number
  type?: string
  protocol?: string
}

export function useAssetOperations() {
  const router = useRouter()

  // 处理资产操作路由跳转
  const handleAssetOperation = (operation: string, asset: Asset) => {
    console.log(`${operation}:`, asset)
    router.push(
      `/wallet/asset?type=${operation}&p=${asset.protocol || 'btc'}&t=${asset.type}&a=${asset.id}`
    )
  }

  // L1 操作
  const handleSend = (asset: Asset) => handleAssetOperation('l1_send', asset)
  const handleDeposit = (asset: Asset) => handleAssetOperation('deposit', asset)
  const handleSplicingIn = (asset: Asset) => handleAssetOperation('splicing_in', asset)

  // L2 操作
  const handleL2Send = (asset: Asset) => handleAssetOperation('l2_send', asset)
  const handleWithdraw = (asset: Asset) => handleAssetOperation('withdraw', asset)
  const handleLock = (asset: Asset) => handleAssetOperation('lock', asset)

  // Channel 操作
  const handleSplicingOut = (asset: Asset) => handleAssetOperation('splicing_out', asset)
  const handleUnlock = (asset: Asset) => handleAssetOperation('unlock', asset)

  return {
    // L1 操作
    handleSend,
    handleDeposit,
    handleSplicingIn,

    // L2 操作
    handleL2Send,
    handleWithdraw,
    handleLock,

    // Channel 操作
    handleSplicingOut,
    handleUnlock
  }
}
