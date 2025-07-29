import { storage } from 'wxt/storage'
import { NodeStakeData } from '@/types'

// 存储键的格式
const getStorageKey = (publicKey: string): `local:node_stake_${string}` => {
  return `local:node_stake_${publicKey}` as `local:node_stake_${string}`
}

class NodeStakeStorage {
  private static instance: NodeStakeStorage | null = null

  private constructor() {}

  public static getInstance(): NodeStakeStorage {
    if (!NodeStakeStorage.instance) {
      NodeStakeStorage.instance = new NodeStakeStorage()
    }
    return NodeStakeStorage.instance
  }

  /**
   * 保存节点质押数据
   * @param publicKey 钱包公钥
   * @param data 质押数据
   */
  async saveNodeStakeData(publicKey: string, data: Omit<NodeStakeData, 'createdAt' | 'expiresAt'>): Promise<void> {
    try {
      const now = Date.now()
      const expiresAt = now + (24 * 60 * 60 * 1000) // 1天后过期
      
      const stakeData: NodeStakeData = {
        ...data,
        createdAt: now,
        expiresAt
      }

      await storage.setItem(getStorageKey(publicKey), stakeData)
      console.log('Node stake data saved for publicKey:', publicKey)
    } catch (error) {
      console.error('Failed to save node stake data:', error)
      throw new Error(`Failed to save node stake data: ${error}`)
    }
  }

  /**
   * 获取节点质押数据
   * @param publicKey 钱包公钥
   * @returns 质押数据或null（如果不存在或已过期）
   */
  async getNodeStakeData(publicKey: string): Promise<NodeStakeData | null> {
    try {
      const data = await storage.getItem<NodeStakeData>(getStorageKey(publicKey))
      
      if (!data) {
        return null
      }

      // 检查是否过期
      const now = Date.now()
      if (now > data.expiresAt) {
        // 数据已过期，删除它
        await this.deleteNodeStakeData(publicKey)
        return null
      }

      return data
    } catch (error) {
      console.error('Failed to get node stake data:', error)
      return null
    }
  }

  /**
   * 删除节点质押数据
   * @param publicKey 钱包公钥
   */
  async deleteNodeStakeData(publicKey: string): Promise<void> {
    try {
      await storage.removeItem(getStorageKey(publicKey))
      console.log('Node stake data deleted for publicKey:', publicKey)
    } catch (error) {
      console.error('Failed to delete node stake data:', error)
      throw new Error(`Failed to delete node stake data: ${error}`)
    }
  }

  /**
   * 检查是否有有效的节点质押数据
   * @param publicKey 钱包公钥
   * @returns 是否有有效的质押数据
   */
  async hasValidNodeStakeData(publicKey: string): Promise<boolean> {
    const data = await this.getNodeStakeData(publicKey)
    return data !== null
  }
}

// 导出单例实例
export const nodeStakeStorage = NodeStakeStorage.getInstance()