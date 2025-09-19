import { ref } from 'vue'
import { Storage } from '@capacitor/storage';

// 简化的推荐人管理 composable
export const useReferrerManager = () => {
  // 生成存储键，只使用地址作为唯一标识符
  const generateStorageKey = (targetAddress: string): any => {
    return `local:referrer_names_${targetAddress}` as const
  }

  // 生成绑定推荐人的存储键
  const generateBoundReferrerKey = (targetAddress: string): any => {
    return `local:bound_referrer_${targetAddress}` as const
  }

  // 获取本地存储的推荐人名字
  const getLocalReferrerNames = async (targetAddress: string): Promise<string[]> => {
    try {
      const key = generateStorageKey(targetAddress)
      const { value } = await Storage.get({ key })
      return value ? JSON.parse(value) : []
    } catch (error) {
      console.error('Failed to get local referrer names:', error)
      return []
    }
  }
  
  // 添加推荐人名字到本地存储
  const addLocalReferrerName = async (targetAddress: string, name: string): Promise<void> => {
    try {
      const key = generateStorageKey(targetAddress)
      const { value } = await Storage.get({ key })
      let names = value ? JSON.parse(value) : []
      if (!names.includes(name)) {
        names.push(name)
        await Storage.set({ key, value: JSON.stringify(names) })
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
      const { value } = await Storage.get({ key })
      if (value) {
        let names = JSON.parse(value)
        names = names.filter(n => n !== name)
        await Storage.set({ key, value: JSON.stringify(names) })
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
      await Storage.remove({ key })
      console.log(`[ReferrerManager] 已清空推荐人名字 (地址: ${targetAddress})`)
    } catch (error) {
      console.error('Failed to clear local referrer names:', error)
      throw error
    }
  }

  // 获取本地存储的绑定推荐人
  const getLocalBoundReferrer = async (targetAddress: string): Promise<string | null> => {
    try {
      const key = generateBoundReferrerKey(targetAddress)
      const { value } = await Storage.get({ key })
      return value || null
    } catch (error) {
      console.error('Failed to get local bound referrer:', error)
      return null
    }
  }

  // 添加绑定推荐人到本地存储
  const addLocalBoundReferrer = async (targetAddress: string, referrerName: string): Promise<void> => {
    try {
      const key = generateBoundReferrerKey(targetAddress)
      await Storage.set({ key, value: referrerName })
      console.log(`[ReferrerManager] 已添加绑定推荐人到本地存储: ${referrerName} (地址: ${targetAddress})`)
    } catch (error) {
      console.error('Failed to add local bound referrer:', error)
      throw error
    }
  }

  // 删除本地存储的绑定推荐人
  const removeLocalBoundReferrer = async (targetAddress: string): Promise<void> => {
    try {
      const key = generateBoundReferrerKey(targetAddress)
      await Storage.remove({ key })
      console.log(`[ReferrerManager] 已删除绑定推荐人 (地址: ${targetAddress})`)
    } catch (error) {
      console.error('Failed to remove local bound referrer:', error)
      throw error
    }
  }
  
  return {
    getLocalReferrerNames,
    addLocalReferrerName,
    removeLocalReferrerName,
    clearLocalReferrerNames,
    getLocalBoundReferrer,
    addLocalBoundReferrer,
    removeLocalBoundReferrer,
  }
}