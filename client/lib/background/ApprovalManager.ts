import { browser } from 'wxt/browser'

interface ApprovalData {
  windowId: number
  eventData: any
}

let currentApproval: ApprovalData | null = null

/**
 * 打开审批窗口（如有旧窗口则先关闭）
 */
export async function openApprovalWindow(eventData: any): Promise<number | null> {
  if (currentApproval) {
    console.log('[ApprovalManager] 关闭旧审批窗口', currentApproval.windowId)
    await closeApprovalWindow()
  }
  const url = browser.runtime.getURL('/popup.html#/wallet/approve')
  const newWindow = await browser.windows.create({ url, type: 'popup', width: 400, height: 600 })
  if (newWindow?.id != null) {
    currentApproval = { windowId: newWindow.id, eventData }
    console.log('[ApprovalManager] 新审批窗口已创建', newWindow.id)
    return newWindow.id
  } else {
    console.error('[ApprovalManager] 审批窗口创建失败')
    return null
  }
}

/**
 * 关闭当前审批窗口
 */
export async function closeApprovalWindow(): Promise<void> {
  if (currentApproval) {
    try {
      await browser.windows.remove(currentApproval.windowId)
      console.log('[ApprovalManager] 审批窗口已关闭', currentApproval.windowId)
    } catch (e) {
      console.warn('[ApprovalManager] 关闭窗口失败，可能已被关闭', e)
    }
    currentApproval = null
  }
}

/**
 * 获取当前审批数据
 */
export function getApprovalData(): ApprovalData | null {
  return currentApproval
}

/**
 * 清除审批数据（不关闭窗口）
 */
export function clearApproval(): void {
  if (currentApproval) {
    console.log('[ApprovalManager] 清除审批数据', currentApproval.windowId)
  }
  currentApproval = null
} 