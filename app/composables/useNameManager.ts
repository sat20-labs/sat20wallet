import { ref, computed, readonly } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { storeToRefs } from 'pinia'
import ordxApi from '@/apis/ordx'
import { useWalletStore } from '@/store'

// 获取名字存储的 key
const getNameStorageKey = (address: string) => `user_name_${address}`

// 名字管理 composable
export const useNameManager = () => {
  const walletStore = useWalletStore()
  const { network, address } = storeToRefs(walletStore)
  
  // 当前选择的名字
  const currentName = ref<string>('')
  
  // 获取指定地址的所有名字列表
  const getNsListByAddress = async (address: string) => {
    try {
      const response = await ordxApi.getNsListByAddress({ address, network: network.value })
      console.log('response', response);
      const { names } = response?.data || {}
      // 处理 API 响应，确保返回正确的格式
      if (names && Array.isArray(names)) {
        return names.map((item: any) => ({
          id: item.id || item.name,
          name: item.name,
          address: item.address || address
        }))
      }
      
      return []
    } catch (error) {
      console.error('Failed to fetch names from ordx API:', error)
      return []
    }
  }

  // 查询名字列表
  const {
    data: nameList,
    isLoading: isLoadingNames,
    error: nameError,
    refetch: refetchNames
  } = useQuery({
    queryKey: ['names', address, network],
    queryFn: () => getNsListByAddress(address.value || ''),
    enabled: computed(() => !!address.value),
  })

  // 获取当前地址保存的名字
  const getCurrentName = async (address: string): Promise<string> => {
    try {
      const savedName = localStorage.getItem(getNameStorageKey(address))
      return savedName || ''
    } catch (error) {
      console.error('Failed to get current name:', error)
      return ''
    }
  }

  // 设置当前地址的名字
  const setCurrentName = async (targetAddress: string, name: string): Promise<void> => {
    try {
      localStorage.setItem(getNameStorageKey(targetAddress), name)
      if (targetAddress === address.value) {
        currentName.value = name
      }
    } catch (error) {
      console.error('Failed to set current name:', error)
      throw error
    }
  }

  // 清空当前地址的名字
  const clearName = async (targetAddress: string): Promise<void> => {
    try {
      localStorage.setItem(getNameStorageKey(targetAddress), '')
      if (targetAddress === address.value) {
        currentName.value = ''
      }
    } catch (error) {
      console.error('Failed to clear name:', error)
      throw error
    }
  }

  // 校验名字是否有效（是否在名字列表中）
  const validateName = async (address: string): Promise<boolean> => {
    try {
      const savedName = await getCurrentName(address)
      if (!savedName) return true // 没有保存名字也算有效

      const names = await getNsListByAddress(address)
      return names.some(item => item.name === savedName)
    } catch (error) {
      console.error('Failed to validate name:', error)
      return false
    }
  }

  // 设置当前地址并加载相关数据
  const setCurrentAddress = async (address: string) => {
    const savedName = await getCurrentName(address)
    currentName.value = savedName
  }

  // 自动校验并清理无效名字
  const validateAndCleanName = async (address: string): Promise<void> => {
    const isValid = await validateName(address)
    if (!isValid) {
      await clearName(address)
    }
  }

  return {
    // 状态
    currentName: readonly(currentName),
    nameList: readonly(nameList),
    isLoadingNames,
    nameError,
    
    // 方法
    getCurrentName,
    setCurrentName,
    clearName,
    validateName,
    setCurrentAddress,
    validateAndCleanName,
    refetchNames,
  }
} 