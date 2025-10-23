/**
 * Eruda 调试工具控制模块
 * 提供手动控制 eruda 启用/禁用的功能
 * 默认启用状态，除非明确禁用
 */

export const ErudaControl = {
  /**
   * 启用 eruda 调试工具
   */
  enable() {
    localStorage.setItem('eruda-debug', 'true')
    console.log('🔧 Eruda 调试模式已启用，刷新页面生效')

    // 如果 eruda 已经加载，直接显示
    if (typeof window !== 'undefined' && (window as any).eruda) {
      ;(window as any).eruda.show()
    }
  },

  /**
   * 禁用 eruda 调试工具
   */
  disable() {
    localStorage.setItem('eruda-debug', 'false')
    console.log('🔧 Eruda 调试模式已禁用，刷新页面生效')

    // 如果 eruda 已经加载，直接隐藏
    if (typeof window !== 'undefined' && (window as any).eruda) {
      ;(window as any).eruda.hide()
    }
  },

  /**
   * 切换 eruda 状态
   */
  toggle() {
    const current = this.isEnabled()
    if (current) {
      this.disable()
    } else {
      this.enable()
    }
    return !current
  },

  /**
   * 检查 eruda 是否启用
   * 默认启用，除非明确设置为 'false'
   */
  isEnabled(): boolean {
    const value = localStorage.getItem('eruda-debug')
    return value !== 'false' // 只有明确设置为 'false' 才算禁用
  },

  /**
   * 重置为默认启用状态
   */
  reset() {
    localStorage.removeItem('eruda-debug')
    console.log('🔧 Eruda 调试模式已重置为默认启用状态')
  },

  /**
   * 获取当前状态描述
   */
  getStatus(): string {
    if (this.isEnabled()) {
      return '🟢 Eruda 调试工具已启用'
    } else {
      return '🔴 Eruda 调试工具已禁用'
    }
  },

  /**
   * 在控制台提供快捷方法
   */
  exposeGlobalMethods() {
    // 暴露到全局对象，方便在控制台调用
    if (typeof window !== 'undefined') {
      ;(window as any).erudaControl = this

      // 显示当前状态和控制命令
      console.log(`
🔧 Eruda 调试工具已集成（默认启用）
${this.getStatus()}

控制命令：
- erudaControl.enable()   // 启用调试工具
- erudaControl.disable()  // 禁用调试工具
- erudaControl.toggle()   // 切换调试状态
- erudaControl.reset()    // 重置为默认启用
- erudaControl.getStatus() // 查看当前状态
- erudaControl.isEnabled() // 检查是否启用

注意：默认启用，刷新页面后自动生效
      `)
    }
  }
}

// 在模块加载时自动暴露全局方法
ErudaControl.exposeGlobalMethods()