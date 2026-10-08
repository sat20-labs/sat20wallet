import { computed, readonly } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { storeToRefs } from 'pinia'
import ordxApi from '@/apis/ordx'
import { useWalletStore } from '@/store'

// 名字管理 composable
export const useNameManager = () => {
  const walletStore = useWalletStore()
  const { network, address } = storeToRefs(walletStore)

  // 当前选择的名字
  const currentName = computed(() => walletStore.wallet?.accounts.find(
    account => account.index === Number(walletStore.accountIndex),
  )?.did || '')

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

  // The SDK catalog is the only persisted source of address DID metadata.
  const getCurrentName = async (targetAddress: string): Promise<string> => {
    if (targetAddress === address.value) return currentName.value
    for (const wallet of walletStore.wallets) {
      const account = wallet.accounts.find(account => account.address === targetAddress)
      if (account) return account.did || ''
    }
    return ''
  }

  const setCurrentName = async (targetAddress: string, name: string): Promise<void> => {
    if (!targetAddress || targetAddress !== address.value) throw new Error('当前钱包地址已变化，请重新打开 DID 设置')
    await walletStore.updateAccountDID(Number(walletStore.accountIndex), name.trim())
  }
  const clearName = (targetAddress: string) => setCurrentName(targetAddress, '')

  // Name ownership queries are advisory; a failed/offline query must never
  // clear a user-confirmed account attribute.
  const validateName = async (targetAddress: string): Promise<boolean> => {
    const savedName = await getCurrentName(targetAddress)
    if (!savedName) return true
    return (await getNsListByAddress(targetAddress)).some(item => item.name === savedName)
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
    refetchNames,
  }
}