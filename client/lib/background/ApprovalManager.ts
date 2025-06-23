import { browser } from 'wxt/browser'
import type { Runtime } from 'wxt/browser'
import { createPopup } from '@/utils/popup'
import { walletError } from '@/types/error'
import { Message } from '@/types/message'

export class ApprovalManager {
  // 储存待处理的审批请求，用窗口ID作为键
  private approveMap = new Map<
    string,
    { windowId: number; eventData: any }
  >()

  // 持有内容脚本的端口引用，以便发回响应
  private contentPort: Runtime.Port | undefined

  constructor() {
    console.log('调试: ApprovalManager 初始化')
    this.handleWindowRemoved = this.handleWindowRemoved.bind(this)
  }

  public setContentPort(port: Runtime.Port | undefined) {
    this.contentPort = port
    console.log(`调试: ApprovalManager 设置 contentPort: ${port ? '可用' : '不可用'}`)
  }

  public async requestApproval(eventData: any) {
    console.log('调试: ApprovalManager 收到审批请求', eventData.action)
    const { origin } = eventData.metadata

    // 在创建新窗口前，先关闭来自同一来源（origin）的旧审批窗口
    const windowsToRemove: number[] = []
    for (const data of this.approveMap.values()) {
      if (data.eventData.metadata.origin === origin) {
        windowsToRemove.push(data.windowId)
      }
    }

    for (const winId of windowsToRemove) {
      console.log(`调试: 关闭之前的审批窗口 ${winId} (来源: ${origin})`)
      try {
        await browser.windows.remove(winId)
      } catch (error) {
         // 这里我们只记录警告，因为窗口可能已经被用户手动关闭了
        console.warn(`调试: 移除旧窗口 ${winId} 失败, 可能已被关闭:`, error)
      }
      // 无论成功与否都从map中删除
      this.approveMap.delete(winId.toString())
    }

    const newWindow = await createPopup(
      browser.runtime.getURL('/popup.html#/wallet/approve'),
    )

    if (newWindow?.id) {
      console.log(`调试: 创建新的审批窗口 ${newWindow.id} (动作: ${eventData.action})`)
      this.approveMap.set(newWindow.id.toString(), {
        windowId: newWindow.id,
        eventData: eventData,
      })
    } else {
      console.error('调试: 创建审批窗口失败')
      throw new Error('创建审批弹窗失败')
    }
  }

  public handleResponse(approved: boolean, windowId: number, data: any) {
    const windowIdStr = windowId.toString()
    const request = this.approveMap.get(windowIdStr)
    if (!request) {
      console.warn(`调试: 收到未知窗口 ${windowId} 的响应，忽略。`)
      return
    }

    if (this.contentPort) {
      const responseEvent = {
        ...request.eventData,
        metadata: {
          ...request.eventData.metadata,
          from: Message.MessageFrom.BACKGROUND,
          to: Message.MessageTo.INJECTED,
        },
        data: approved ? data : null,
        error: approved ? null : walletError.userReject,
      }
      console.log(`调试: 发送审批结果到内容脚本 (窗口ID: ${windowId}, 结果: ${approved})`, responseEvent)
      this.contentPort.postMessage(responseEvent)
    } else {
      console.error('调试: 无法发送审批响应, 内容脚本端口不可用')
    }

    this.cleanup(windowId)
  }

  public handleWindowRemoved(closedWindowId: number) {
    const windowIdStr = closedWindowId.toString()
    if (this.approveMap.has(windowIdStr)) {
      console.log(`调试: 审批窗口 ${closedWindowId} 被用户关闭, 视为拒绝。`)
      this.handleResponse(false, closedWindowId, null)
    }
  }

  public getApprovalData(windowId: number) {
    const approveData = this.approveMap.get(windowId.toString())
    if (approveData) {
      console.log(`调试: 为弹窗 ${windowId} 提供审批数据`)
      return {
        action: Message.MessageAction.GET_APPROVE_DATA_RESPONSE,
        data: approveData.eventData,
      }
    }
    console.warn(`调试: 弹窗 ${windowId} 请求数据, 但未找到待审批项`)
    return undefined
  }

  private cleanup(windowId: number) {
    const windowIdStr = windowId.toString()
    this.approveMap.delete(windowIdStr)
    try {
      browser.windows.remove(windowId)
      console.log(`调试: 清理并关闭窗口 ${windowId}`)
    } catch (e) {
      // 窗口可能已经关闭，这是正常现象
    }
  }
} 