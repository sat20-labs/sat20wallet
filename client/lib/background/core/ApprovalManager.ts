import { Message } from '@/types/message'
import { browser } from 'wxt/browser'
import { createPopup } from '@/utils/popup'
import { walletError } from '@/types/error'

/**
 * 审批管理器 - 负责管理需要用户确认的操作
 */
export class ApprovalManager {
  // 需要审批的操作列表
  private readonly REQUIRES_APPROVAL = [
    Message.MessageAction.REQUEST_ACCOUNTS,
    Message.MessageAction.SWITCH_NETWORK,
    Message.MessageAction.SEND_BITCOIN,
    Message.MessageAction.SIGN_MESSAGE,
    Message.MessageAction.SIGN_PSBT,
    Message.MessageAction.SIGN_PSBTS,
    Message.MessageAction.SEND_INSCRIPTION,
    Message.MessageAction.SPLIT_ASSET,
    // UTXO操作
    Message.MessageAction.LOCK_UTXO,
    Message.MessageAction.UNLOCK_UTXO,
    Message.MessageAction.UNLOCK_UTXO_SATSNET,
    // 通道操作
    Message.MessageAction.LOCK_TO_CHANNEL,
    Message.MessageAction.UNLOCK_FROM_CHANNEL,
    Message.MessageAction.BATCH_SEND_ASSETS_SATSNET,
    // 合约相关
    Message.MessageAction.DEPLOY_CONTRACT_REMOTE,
    Message.MessageAction.INVOKE_CONTRACT_SATSNET,
  ]

  // 存储待审批的请求，以窗口ID为键
  private approveMap = new Map<string, { windowId: number; eventData: any }>()

  /**
   * 检查指定操作是否需要审批
   */
  requiresApproval(action: string): boolean {
    return this.REQUIRES_APPROVAL.includes(action as any)
  }

  /**
   * 创建审批窗口
   */
  async createApprovalWindow(eventData: any, origin: string): Promise<void> {
    // 关闭同一来源的现有审批窗口
    await this.closeExistingApprovalWindows(origin)

    try {
      const newWindow = await createPopup(
        browser.runtime.getURL('/popup.html#/wallet/approve')
      )

      if (newWindow?.id) {
        this.approveMap.set(newWindow.id.toString(), {
          windowId: newWindow.id,
          eventData: eventData,
        })
        console.log(`Approval window ${newWindow.id} created for action ${eventData.action}`)
      } else {
        throw new Error('Failed to create approval window')
      }
    } catch (error) {
      console.error('Failed to create approval window:', error)
      throw error
    }
  }

  /**
   * 关闭指定来源的现有审批窗口
   */
  private async closeExistingApprovalWindows(origin: string): Promise<void> {
    const windowsToRemove: number[] = []
    
    for (const [windowIdStr, data] of this.approveMap.entries()) {
      if (data.eventData.metadata.origin === origin) {
        windowsToRemove.push(data.windowId)
      }
    }

    for (const winId of windowsToRemove) {
      try {
        await browser.windows.remove(winId)
        this.approveMap.delete(winId.toString())
        console.log(`Closed previous approval window ${winId} for origin ${origin}`)
      } catch (error) {
        console.warn(`Failed to remove previous window ${winId}, maybe already closed:`, error)
        this.approveMap.delete(winId.toString())
      }
    }
  }

  /**
   * 获取审批数据
   */
  getApprovalData(windowId: string): any {
    const approveData = this.approveMap.get(windowId)
    return approveData ? approveData.eventData : null
  }

  /**
   * 处理审批响应
   */
  handleApprovalResponse(windowId: number, approved: boolean): any {
    const windowIdStr = windowId.toString()
    const approveData = this.approveMap.get(windowIdStr)
    
    if (!approveData) {
      console.warn(`No approval data found for window ${windowId}`)
      return null
    }

    const responseData = {
      ...approveData.eventData,
      metadata: {
        ...approveData.eventData.metadata,
        from: Message.MessageFrom.BACKGROUND,
        to: Message.MessageTo.INJECTED,
      },
    }

    if (!approved) {
      responseData.data = null
      responseData.error = walletError.userReject
    }

    // 清理审批数据
    this.approveMap.delete(windowIdStr)
    
    // 关闭窗口
    this.closeWindow(windowId)

    return responseData
  }

  /**
   * 处理窗口关闭事件
   */
  handleWindowClosed(closedWindowId: number): any {
    const windowIdStr = closedWindowId.toString()
    const approveData = this.approveMap.get(windowIdStr)
    
    if (!approveData) {
      return null
    }

    console.log(`Rejecting request via window close for window ${closedWindowId}`)
    
    const responseData = {
      ...approveData.eventData,
      metadata: {
        ...approveData.eventData.metadata,
        from: Message.MessageFrom.BACKGROUND,
        to: Message.MessageTo.INJECTED,
      },
      data: null,
      error: walletError.userReject,
    }

    // 清理审批数据
    this.approveMap.delete(windowIdStr)

    return responseData
  }

  /**
   * 关闭窗口
   */
  private async closeWindow(windowId: number): Promise<void> {
    try {
      await browser.windows.remove(windowId)
    } catch (error) {
      console.warn(`Window ${windowId} might already be closed:`, error)
    }
  }

  /**
   * 获取所有待审批的请求
   */
  getPendingApprovals(): Map<string, { windowId: number; eventData: any }> {
    return new Map(this.approveMap)
  }

  /**
   * 清理所有待审批的请求
   */
  clearAllApprovals(): void {
    for (const [windowIdStr, data] of this.approveMap.entries()) {
      this.closeWindow(data.windowId)
    }
    this.approveMap.clear()
  }

  /**
   * 添加需要审批的方法
   */
  addApprovalRequiredMethod(action: string): void {
    if (!this.REQUIRES_APPROVAL.includes(action as any)) {
      (this.REQUIRES_APPROVAL as string[]).push(action)
    }
  }

  /**
   * 移除需要审批的方法
   */
  removeApprovalRequiredMethod(action: string): void {
    const index = this.REQUIRES_APPROVAL.indexOf(action as any)
    if (index > -1) {
      (this.REQUIRES_APPROVAL as string[]).splice(index, 1)
    }
  }

  /**
   * 获取所有需要审批的方法列表
   */
  getApprovalRequiredMethods(): readonly string[] {
    return this.REQUIRES_APPROVAL
  }
}