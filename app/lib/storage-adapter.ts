/**
 * localStorage 适配器，提供与 @capacitor/storage 相同的接口
 * 用于在纯 web 环境下替换 @capacitor/storage
 */
export const Storage = {
  async get({ key }: { key: string }): Promise<{ value: string | null }> {
    try {
      const value = localStorage.getItem(key)
      return { value }
    } catch (error) {
      console.error('localStorage.getItem error:', error)
      return { value: null }
    }
  },

  async set({ key, value }: { key: string; value: string }): Promise<void> {
    try {
      localStorage.setItem(key, value)
    } catch (error) {
      console.error('localStorage.setItem error:', error)
      throw error
    }
  },

  async remove({ key }: { key: string }): Promise<void> {
    try {
      localStorage.removeItem(key)
    } catch (error) {
      console.error('localStorage.removeItem error:', error)
      throw error
    }
  },

  async clear(): Promise<void> {
    try {
      // 只清除钱包相关的 localStorage 项，而不是全部清除
      const keysToRemove: string[] = []
      for (let i = 0; i < localStorage.length; i++) {
        const key = localStorage.key(i)
        if (key && (key.startsWith('local:wallet_') || key.startsWith('session:wallet_') || key.startsWith('authorized_origins') || key.startsWith('node_stake_') || key.startsWith('referrer_'))) {
          keysToRemove.push(key)
        }
      }
      keysToRemove.forEach(key => localStorage.removeItem(key))
    } catch (error) {
      console.error('localStorage.clear error:', error)
      throw error
    }
  }
}
