import { defineStore } from 'pinia'
import { config as configMap } from '@/config'
import { walletStorage } from '@/lib/walletStorage'

export type Env = 'dev' | 'test' | 'prod'
export const useGlobalStore = defineStore('global', () => {
  
  const loading = ref(false)
  const version = ref(0)
  const env = ref<Env>(walletStorage.getValue('env') || 'test')
  const stpVersion = ref('0.0.0')

  const config = computed(() => {
    return configMap[env.value]
  })
  const setStpVersion = (value: string) => {
    stpVersion.value = value
  }
  const setVersion = (value: number) => {
    version.value = value
  }
  const setLoading = (value: boolean) => {
    loading.value = value
  }
  const setEnv = async (value: Env) => {
    env.value = value
    await walletStorage.setValue('env', value)
  }
  return {
    loading,
    setLoading,
    version,
    setVersion,
    stpVersion,
    setStpVersion,
    env,
    setEnv,
    config,
  }
})
