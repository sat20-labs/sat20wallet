import { defineStore } from 'pinia'
import { walletStorage } from '@/lib/walletStorage'

export const useGlobalStore = defineStore('global', () => {
  
  const loading = ref(false)
  const version = ref(0)
  const stpVersion = ref('0.0.0')
  const setStpVersion = (value: string) => {
    stpVersion.value = value
  }
  const setVersion = (value: number) => {
    version.value = value
  }
  const setLoading = (value: boolean) => {
    loading.value = value
  }
  return {
    loading,
    setLoading,
    version,
    setVersion,
    stpVersion,
    setStpVersion,
  }
})
