import { defineStore } from 'pinia'
import { ref, computed } from 'vue'

interface Asset {
  type: string      // BTC, ORDX, Runes, BRC20
  ticker?: string   // 具体资产的 ticker，如 Pearl, SATS 等
  name?: string     // 资产名称
  icon?: string     // 资产图标
}

interface Pool {
  id: string
  asset: Asset
  totalValue: number    // sats
  totalAmount?: number  // 非 BTC 资产的数量
  userCount: number
  minDeposit: number    // sats
  unit: string
}

interface UserPool {
  id: string
  asset: Asset
  balance: number       // sats
  amount?: number       // 非 BTC 资产的数量
  unit: string
}

export const usePoolStore = defineStore('pool', () => {
  // State
  const currentPool = ref<UserPool[]>([])
  const availablePools = ref<Pool[]>([])
  const isLoading = ref(false)

  // Getters
  const canCreatePool = computed(() => {
    // TODO: Add logic to check if user can create pool
    return true
  })

  // Actions
  const fetchPools = async () => {
    try {
      isLoading.value = true
      // TODO: Add API call to fetch pools
      availablePools.value = [
        {
          id: '1',
          asset: {
            type: 'ORDX',
            ticker: 'RarePizza',
            name: 'RarePizza',
            icon: 'lucide:pizza'
          },
          totalValue: 5000000,
          totalAmount: 10000,
          userCount: 10,
          minDeposit: 100000,
          unit: 'sats'
        },
        {
          id: '2',
          asset: {
            type: 'Runes',
            ticker: 'Doge',
            name: 'Doge Rune',
            icon: 'lucide:dog'
          },
          totalValue: 3000000,
          totalAmount: 10000,
          userCount: 5,
          minDeposit: 50000,
          unit: 'sats'
        },
        {
          id: '3',
          asset: {
            type: 'BRC20',
            ticker: 'SATS',
            name: 'Sats Token',
            icon: 'lucide:server-cog'
          },
          totalValue: 5000000,
          totalAmount: 500,
          userCount: 5,
          minDeposit: 150000,
          unit: 'sats'
        }
      ]
    } catch (error) {
      console.error('Error fetching pools:', error)
    } finally {
      isLoading.value = false
    }
  }

  const fetchCurrentPool = async () => {
    try {
      isLoading.value = true
      // TODO: Add API call to fetch current pool
      currentPool.value = [{
        id: '1',
        asset: {
          type: 'BTC',
          name: 'Bitcoin',
          icon: 'cryptocurrency:btc'
        },
        balance: 1000000,
        unit: 'sats'
      },
      {
        id: '2',
        asset: {
          type: 'ORDX',
          ticker: 'Pearl',
          name: 'Pearl',
          icon: 'lucide:gem'
        },
        balance: 500000,
        amount: 10000,
        unit: 'sats'
      }]
    } catch (error) {
      console.error('Error fetching current pool:', error)
      currentPool.value = []
    } finally {
      isLoading.value = false
    }
  }

  const joinPool = async (poolId: string, amount: number) => {
    try {
      isLoading.value = true
      // TODO: Add API call to join pool
      await new Promise(resolve => setTimeout(resolve, 1000))
      await fetchCurrentPool()
      return true
    } catch (error) {
      console.error('Error joining pool:', error)
      return false
    } finally {
      isLoading.value = false
    }
  }

  const exitPool = async () => {
    try {
      isLoading.value = true
      // TODO: Add API call to exit pool
      await new Promise(resolve => setTimeout(resolve, 1000))
      currentPool.value = []
      return true
    } catch (error) {
      console.error('Error exiting pool:', error)
      return false
    } finally {
      isLoading.value = false
    }
  }

  const createPool = async (assetType: string, minDeposit: number) => {
    try {
      isLoading.value = true
      // TODO: Add API call to create pool
      await new Promise(resolve => setTimeout(resolve, 1000))
      await fetchPools()
      return true
    } catch (error) {
      console.error('Error creating pool:', error)
      return false
    } finally {
      isLoading.value = false
    }
  }

  return {
    // State
    currentPool,
    availablePools,
    isLoading,

    // Getters
    canCreatePool,

    // Actions
    fetchPools,
    fetchCurrentPool,
    joinPool,
    exitPool,
    createPool
  }
})
