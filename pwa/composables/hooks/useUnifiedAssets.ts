import { computed } from 'vue'
import { useL1Assets } from './useL1Assets'
import { useRgb11Assets } from './useRgb11Assets'

interface UseAssetQueryOptions {
  enabled?: boolean | { value: boolean }
}

export const useUnifiedAssets = (options: UseAssetQueryOptions = {}) => {
  const requestedEnabled = computed(() => {
    const enabled = options.enabled
    if (typeof enabled === 'boolean') return enabled
    return enabled?.value ?? true
  })

  const rgb11 = useRgb11Assets({ enabled: requestedEnabled })
  const l1Enabled = computed(() => requestedEnabled.value && rgb11.ready.value)
  const l1 = useL1Assets({ enabled: l1Enabled })

  const refreshUnifiedAssets = async (refreshOptions: any = {}) => {
    await rgb11.refreshRGB11Assets()
    await l1.refreshL1Assets(refreshOptions)
  }

  return {
    loading: computed(() => rgb11.loading.value || (rgb11.ready.value && l1.loading.value)),
    error: rgb11.error,
    refreshL1Assets: refreshUnifiedAssets,
    refreshUnifiedAssets,
  }
}
