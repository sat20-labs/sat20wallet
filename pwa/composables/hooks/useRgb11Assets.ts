import { computed, onScopeDispose, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { storeToRefs } from 'pinia'
import walletManager from '@/utils/sat20'
import { useGlobalStore, useRGB11Store, useWalletStore } from '@/store'
import type { RGBView, RGBAssetView } from '@/utils/walletViews'

interface UseAssetQueryOptions {
  enabled?: boolean | { value: boolean }
}

export const decorateRGB11AssetItems = (items: RGBAssetView[]) => items.map(item => {
  const [protocol, type, ticker] = item.asset.split(':')
  return { ...item, protocol, type, ticker, id: item.asset, key: item.asset, asset_key: item.asset,
    label: item.ticker || item.name || item.asset, symbol: item.ticker, display_name: item.name,
    amount: item.available, balance: item.available, utxos: [], available_amount: item.available, pending_amount: item.pending }
})

export const useRgb11Assets = (options: UseAssetQueryOptions = {}) => {
  const walletStore = useWalletStore()
  const globalStore = useGlobalStore()
  const rgb11Store = useRGB11Store()
  const { walletId, accountIndex, network, address } = storeToRefs(walletStore)
  const { env } = storeToRefs(globalStore)

  const queryEnabled = computed(() => {
    const enabled = options.enabled
    if (typeof enabled === 'boolean') return enabled
    return enabled?.value ?? true
  })

  const stateQuery = useQuery({
    queryKey: ['rgb11-state', walletId, accountIndex, network, address, env],
    queryFn: async (): Promise<RGBView> => {
      const [err, result] = await walletManager.getRGB11View()
      if (err) throw err
      if (!result) throw new Error('RGB wallet state is unavailable')
      return result
    },
    enabled: computed(() => queryEnabled.value && !!walletId.value && !!address.value),
  })

  watch(
    () => stateQuery.data.value,
    (state) => {
      if (!state) return
      rgb11Store.setState(state)
    },
    { deep: true, immediate: true }
  )

  watch(
    () => stateQuery.error.value,
    (error) => {
      rgb11Store.setError(error instanceof Error ? error.message : String(error || ''))
    },
    { immediate: true }
  )

  const handleWalletDataUpdated = () => {
    if (queryEnabled.value && walletId.value && address.value) {
      void stateQuery.refetch()
    }
  }
  window.addEventListener('sat20:wallet-data-updated', handleWalletDataUpdated)
  onScopeDispose(() => {
    window.removeEventListener('sat20:wallet-data-updated', handleWalletDataUpdated)
  })

  return {
    loading: computed(() => stateQuery.isLoading.value),
    ready: computed(() => stateQuery.isSuccess.value),
    error: computed(() => stateQuery.error.value),
    refreshRGB11Assets: async () => {
      const result = await stateQuery.refetch()
      if (result.error) throw result.error
    },
  }
}
