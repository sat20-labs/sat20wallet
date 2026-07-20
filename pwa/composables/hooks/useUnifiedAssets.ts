import { computed } from 'vue'
import { useL1Assets } from './useL1Assets'
import { useRgb11Assets } from './useRgb11Assets'

interface UseAssetQueryOptions {
  enabled?: boolean | { value: boolean }
}

export const useUnifiedAssets = (options: UseAssetQueryOptions = {}) => {
  const l1 = useL1Assets(options)
  const rgb11 = useRgb11Assets(options)

  const refreshUnifiedAssets = async (refreshOptions: any = {}) => {
    await l1.refreshL1Assets(refreshOptions)
    await rgb11.refreshRGB11Assets()
  }

  return {
    loading: computed(() => l1.loading.value || rgb11.loading.value),
    refreshL1Assets: refreshUnifiedAssets,
    refreshUnifiedAssets,
  }
}
