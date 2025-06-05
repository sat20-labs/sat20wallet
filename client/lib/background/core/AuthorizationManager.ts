import { Message } from '@/types/message'
import { isOriginAuthorized } from '@/lib/authorized-origins'

/**
 * 权限管理器 - 负责权限验证和授权管理
 */
export class AuthorizationManager {
  // 需要授权的方法列表
  private readonly METHODS_REQUIRING_AUTHORIZATION = [
    Message.MessageAction.GET_ACCOUNTS,
    Message.MessageAction.GET_PUBLIC_KEY,
    Message.MessageAction.GET_BALANCE,
    Message.MessageAction.GET_NETWORK,
    Message.MessageAction.BUILD_BATCH_SELL_ORDER,
    Message.MessageAction.SPLIT_BATCH_SIGNED_PSBT,
    Message.MessageAction.SPLIT_BATCH_SIGNED_PSBT_SATSNET,
    Message.MessageAction.SEND_BITCOIN,
    Message.MessageAction.SIGN_MESSAGE,
    Message.MessageAction.SIGN_PSBT,
    Message.MessageAction.SIGN_PSBTS,
    Message.MessageAction.PUSH_TX,
    Message.MessageAction.PUSH_PSBT,
    Message.MessageAction.GET_INSCRIPTIONS,
    Message.MessageAction.SEND_INSCRIPTION,
    Message.MessageAction.SWITCH_NETWORK,
    Message.MessageAction.FINALIZE_SELL_ORDER,
    Message.MessageAction.ADD_INPUTS_TO_PSBT,
    Message.MessageAction.ADD_OUTPUTS_TO_PSBT,
    Message.MessageAction.EXTRACT_TX_FROM_PSBT,
    Message.MessageAction.EXTRACT_TX_FROM_PSBT_SATSNET,
    Message.MessageAction.SPLIT_ASSET,
    // UTXO相关操作
    Message.MessageAction.LOCK_UTXO,
    Message.MessageAction.LOCK_UTXO_SATSNET,
    Message.MessageAction.UNLOCK_UTXO,
    Message.MessageAction.UNLOCK_UTXO_SATSNET,
    Message.MessageAction.GET_ALL_LOCKED_UTXO,
    Message.MessageAction.GET_ALL_LOCKED_UTXO_SATSNET,
    Message.MessageAction.LOCK_TO_CHANNEL,
    Message.MessageAction.UNLOCK_FROM_CHANNEL,
    // UTXO获取操作
    Message.MessageAction.GET_UTXOS,
    Message.MessageAction.GET_UTXOS_SATSNET,
    Message.MessageAction.GET_UTXOS_WITH_ASSET,
    Message.MessageAction.GET_UTXOS_WITH_ASSET_SATSNET,
    Message.MessageAction.GET_UTXOS_WITH_ASSET_V2,
    Message.MessageAction.GET_UTXOS_WITH_ASSET_V2_SATSNET,
    Message.MessageAction.GET_ASSET_AMOUNT,
    Message.MessageAction.GET_ASSET_AMOUNT_SATSNET,
    Message.MessageAction.MERGE_BATCH_SIGNED_PSBT,
  ]

  // 权限缓存，避免重复验证
  private authCache = new Map<string, { authorized: boolean; timestamp: number }>()
  private readonly CACHE_DURATION = 5 * 60 * 1000 // 5分钟缓存

  /**
   * 检查指定操作是否需要授权
   */
  requiresAuthorization(action: string): boolean {
    return this.METHODS_REQUIRING_AUTHORIZATION.includes(action as any)
  }

  /**
   * 检查来源是否已授权
   */
  async isAuthorized(origin: string): Promise<boolean> {
    // 检查缓存
    const cached = this.authCache.get(origin)
    if (cached && Date.now() - cached.timestamp < this.CACHE_DURATION) {
      return cached.authorized
    }

    // 执行权限验证
    const authorized = await isOriginAuthorized(origin)
    
    // 更新缓存
    this.authCache.set(origin, {
      authorized,
      timestamp: Date.now()
    })

    return authorized
  }

  /**
   * 清除指定来源的权限缓存
   */
  clearAuthCache(origin?: string): void {
    if (origin) {
      this.authCache.delete(origin)
    } else {
      this.authCache.clear()
    }
  }

  /**
   * 清理过期的缓存条目
   */
  cleanupExpiredCache(): void {
    const now = Date.now()
    for (const [origin, cache] of this.authCache.entries()) {
      if (now - cache.timestamp >= this.CACHE_DURATION) {
        this.authCache.delete(origin)
      }
    }
  }

  /**
   * 获取所有需要授权的方法列表
   */
  getAuthorizationRequiredMethods(): readonly string[] {
    return this.METHODS_REQUIRING_AUTHORIZATION
  }

  /**
   * 添加需要授权的方法
   */
  addAuthorizationRequiredMethod(action: string): void {
    if (!this.METHODS_REQUIRING_AUTHORIZATION.includes(action as any)) {
      (this.METHODS_REQUIRING_AUTHORIZATION as string[]).push(action)
    }
  }

  /**
   * 移除需要授权的方法
   */
  removeAuthorizationRequiredMethod(action: string): void {
    const index = this.METHODS_REQUIRING_AUTHORIZATION.indexOf(action as any)
    if (index > -1) {
      (this.METHODS_REQUIRING_AUTHORIZATION as string[]).splice(index, 1)
    }
  }
}