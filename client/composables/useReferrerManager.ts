import { ref } from 'vue'
import { storage } from 'wxt/storage'

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

  // 生成绑定推荐人txId的存储键
  const generateBoundReferrerTxIdKey = (targetAddress: string): any => {
    return `local:bound_referrer_txid_${targetAddress}` as const
  }

  // 生成推荐人注册txId的存储键
  const generateReferrerTxIdKey = (targetAddress: string, referrerName: string): any => {
    return `local:referrer_txid_${targetAddress}_${referrerName}` as const
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

  // 获取本地存储的绑定推荐人
  const getLocalBoundReferrer = async (targetAddress: string): Promise<string | null> => {
    try {
      const key = generateBoundReferrerKey(targetAddress)
      const boundReferrer = await storage.getItem<string>(key)
      return boundReferrer || null
    } catch (error) {
      console.error('Failed to get local bound referrer:', error)
      return null
    }
  }

  // 添加绑定推荐人到本地存储
  const addLocalBoundReferrer = async (targetAddress: string, referrerName: string): Promise<void> => {
    try {
      const key = generateBoundReferrerKey(targetAddress)
      await storage.setItem(key, referrerName)
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
      await storage.removeItem(key)
      console.log(`[ReferrerManager] 已删除绑定推荐人 (地址: ${targetAddress})`)
    } catch (error) {
      console.error('Failed to remove local bound referrer:', error)
      throw error
    }
  }

  // 缓存推荐人注册的txId
  const cacheReferrerTxId = async (targetAddress: string, referrerName: string, txId: string): Promise<void> => {
    try {
      const key = generateReferrerTxIdKey(targetAddress, referrerName)
      await storage.setItem(key, txId)
      console.log(`[ReferrerManager] 已缓存推荐人注册txId: ${txId} (推荐人: ${referrerName}, 地址: ${targetAddress})`)
    } catch (error) {
      console.error('Failed to cache referrer txId:', error)
      throw error
    }
  }

  // 获取推荐人注册的txId
  const getReferrerTxId = async (targetAddress: string, referrerName: string): Promise<string | null> => {
    try {
      const key = generateReferrerTxIdKey(targetAddress, referrerName)
      const txId = await storage.getItem<string>(key)
      return txId || null
    } catch (error) {
      console.error('Failed to get referrer txId:', error)
      return null
    }
  }

  // 获取所有推荐人的txId映射（只返回有txId的推荐人）
  const getAllReferrerTxIds = async (targetAddress: string): Promise<Record<string, string>> => {
    try {
      const names = await getLocalReferrerNames(targetAddress)
      const txIds: Record<string, string> = {}
      const validNames: string[] = []
      
      for (const name of names) {
        const txId = await getReferrerTxId(targetAddress, name)
        if (txId) {
          txIds[name] = txId
          validNames.push(name)
        }
      }
      
      // 如果本地存储的名字中有没有txId的，需要清理掉
      if (validNames.length !== names.length) {
        await storage.setItem(generateStorageKey(targetAddress), validNames)
        console.log(`[ReferrerManager] 已清理无效的推荐人缓存，保留有效名字: ${validNames.join(', ')}`)
      }
      
      return txIds
    } catch (error) {
      console.error('Failed to get all referrer txIds:', error)
      return {}
    }
  }

  // 缓存绑定推荐人的txId
  const cacheBoundReferrerTxId = async (targetAddress: string, txId: string): Promise<void> => {
    try {
      const key = generateBoundReferrerTxIdKey(targetAddress)
      await storage.setItem(key, txId)
      console.log(`[ReferrerManager] 已缓存绑定推荐人txId: ${txId} (地址: ${targetAddress})`)
    } catch (error) {
      console.error('Failed to cache bound referrer txId:', error)
      throw error
    }
  }

  // 获取绑定推荐人的txId
  const getLocalBoundReferrerTxId = async (targetAddress: string): Promise<string | null> => {
    try {
      const key = generateBoundReferrerTxIdKey(targetAddress)
      const txId = await storage.getItem<string>(key)
      return txId || null
    } catch (error) {
      console.error('Failed to get bound referrer txId:', error)
      return null
    }
  }

  // 删除绑定推荐人的txId
  const removeBoundReferrerTxId = async (targetAddress: string): Promise<void> => {
    try {
      const key = generateBoundReferrerTxIdKey(targetAddress)
      await storage.removeItem(key)
      console.log(`[ReferrerManager] 已删除绑定推荐人txId (地址: ${targetAddress})`)
    } catch (error) {
      console.error('Failed to remove bound referrer txId:', error)
      throw error
    }
  }

  // 清理无效的推荐人缓存（没有txId的推荐人名字）
  const cleanInvalidReferrerCache = async (targetAddress: string): Promise<void> => {
    try {
      const names = await getLocalReferrerNames(targetAddress)
      const validNames: string[] = []
      
      for (const name of names) {
        const txId = await getReferrerTxId(targetAddress, name)
        if (txId) {
          validNames.push(name)
        } else {
          // 删除没有txId的推荐人名字
          await removeLocalReferrerName(targetAddress, name)
          console.log(`[ReferrerManager] 已清理无效推荐人缓存: ${name}`)
        }
      }
      
      console.log(`[ReferrerManager] 推荐人缓存清理完成，有效名字: ${validNames.join(', ')}`)
    } catch (error) {
      console.error('Failed to clean invalid referrer cache:', error)
      throw error
    }
  }

  // 清理指定推荐人名字的缓存（当服务器有数据时使用）
  const clearReferrerNameCache = async (targetAddress: string, referrerName: string): Promise<void> => {
    try {
      await removeLocalReferrerName(targetAddress, referrerName)
      // 同时删除对应的txId缓存
      const txIdKey = generateReferrerTxIdKey(targetAddress, referrerName)
      await storage.removeItem(txIdKey)
      console.log(`[ReferrerManager] 已清理推荐人缓存: ${referrerName}`)
    } catch (error) {
      console.error('Failed to clear referrer name cache:', error)
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
    cacheReferrerTxId,
    getReferrerTxId,
    getAllReferrerTxIds,
    cacheBoundReferrerTxId,
    getLocalBoundReferrerTxId,
    removeBoundReferrerTxId,
    cleanInvalidReferrerCache,
    clearReferrerNameCache,
  }
}