import { ref } from 'vue'
import { storage } from 'wxt/storage'

// 简化的推荐人管理 composable
export const useReferrerManager = () => {
  // 生成存储键，只使用地址作为唯一标识符
  const generateStorageKey = (targetAddress: string): any => {
    return `local:referrer_names_${targetAddress}` as const
  }

  // 获取本地存储的推荐人名字
  const getLocalReferrerNames = async (targetAddress: string): Promise<string[]> => {
    try {
      const key = generateStorageKey(targetAddress)
      const names = await storage.getItem<string[]>(key)
      return names || []
    } catch (error) {
      console.error('Failed to get local referrer names:', error)
      return []
    }
  }
  
  // 添加推荐人名字到本地存储
  const addLocalReferrerName = async (targetAddress: string, name: string): Promise<void> => {
    try {
      const key = generateStorageKey(targetAddress)
      let names = await storage.getItem<string[]>(key)
      if (!names) names = []
      if (!names.includes(name)) {
        names.push(name)
        await storage.setItem(key, names)
        console.log(`[ReferrerManager] 已添加推荐人名字到本地存储: ${name} (地址: ${targetAddress})`)
      }
    } catch (error) {
      console.error('Failed to add local referrer name:', error)
      throw error
    }
  }

  // 删除指定地址的推荐人名字
  const removeLocalReferrerName = async (targetAddress: string, name: string): Promise<void> => {
    try {
      const key = generateStorageKey(targetAddress)
      let names = await storage.getItem<string[]>(key)
      if (names) {
        names = names.filter(n => n !== name)
        await storage.setItem(key, names)
        console.log(`[ReferrerManager] 已删除推荐人名字: ${name} (地址: ${targetAddress})`)
      }
    } catch (error) {
      console.error('Failed to remove local referrer name:', error)
      throw error
    }
  }

  // 清空指定地址的所有推荐人名字
  const clearLocalReferrerNames = async (targetAddress: string): Promise<void> => {
    try {
      const key = generateStorageKey(targetAddress)
      await storage.removeItem(key)
      console.log(`[ReferrerManager] 已清空推荐人名字 (地址: ${targetAddress})`)
    } catch (error) {
      console.error('Failed to clear local referrer names:', error)
      throw error
    }
  }
  
  return {
    getLocalReferrerNames,
    addLocalReferrerName,
    removeLocalReferrerName,
    clearLocalReferrerNames,
  }
}