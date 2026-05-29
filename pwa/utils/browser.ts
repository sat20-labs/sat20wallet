import { generateMempoolUrl } from '@/utils'

/**
 * 浏览器工具类，用于在不同环境中打开链接
 */
export class BrowserUtil {
  /**
   * 打开链接
   * @param url 要打开的链接
   * @param options 打开选项
   */
  static async openLink(
    url: string,
    options: {
      // 用于 window.open 的 target，默认为 '_blank'
      target?: string
      // 用于 window.open 的窗口特性，默认为 'noopener,noreferrer'
      windowFeatures?: string
    } = {}
  ): Promise<void> {
    const {
      target = '_blank',
      windowFeatures = 'noopener,noreferrer',
    } = options

    try {
      const newWindow = window.open(url, target, windowFeatures)

      if (!newWindow) {
        console.warn('无法打开新窗口，可能是由于弹窗阻止器')
        window.location.href = url
      }
    } catch (error) {
      console.error('打开链接时发生错误:', error)

      // 作为后备，在当前窗口打开
      try {
        window.location.href = url
      } catch (fallbackError) {
        console.error('无法打开链接:', fallbackError)
        throw new Error(`无法打开链接: ${url}`)
      }
    }
  }

  /**
   * 打开 mempool 链接
   * @param network 网络类型
   * @param path 路径
   * @param options 额外选项
   */
  static async openMempoolLink(
    network: string,
    path: string,
    options?: Omit<Parameters<typeof BrowserUtil.openLink>[1], 'url'>
  ): Promise<void> {
    const url = generateMempoolUrl({
      network,
      path
    })

    return BrowserUtil.openLink(url, options)
  }

  /**
   * 关闭浏览器（仅在 Capacitor 环境中有效）
   */
  static async close(): Promise<void> {
    window.close()
  }

  /**
   * 检查是否在 Capacitor 环境中
   */
  static isCapacitorApp(): boolean {
    return false
  }
}

/**
 * 便捷的链接打开函数
 * @param url 要打开的链接
 * @param options 选项
 */
export const openLink = BrowserUtil.openLink.bind(BrowserUtil)

/**
 * 便捷的 mempool 链接打开函数
 * @param network 网络类型
 * @param path 路径
 * @param options 额外选项
 */
export const openMempoolLink = BrowserUtil.openMempoolLink.bind(BrowserUtil)
