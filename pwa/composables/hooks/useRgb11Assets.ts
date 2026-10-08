import { computed, onScopeDispose, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { storeToRefs } from 'pinia'
import walletManager from '@/utils/sat20'
import { useGlobalStore, useRGB11Store, useWalletStore } from '@/store'
import type { RGB11StateDTO } from '@/store/rgb11'

interface UseAssetQueryOptions {
  enabled?: boolean | { value: boolean }
}

const assetNameOf = (value: any) => value?.Name || value?.name || value?.AssetName || {}

const tickerInfoFor = (state: RGB11StateDTO, name: any) => (
  (state.ticker_infos || []).find((info: any) => {
    const infoName = assetNameOf(info)
    return infoName?.Protocol === name?.Protocol &&
      infoName?.Type === name?.Type &&
      infoName?.Ticker === name?.Ticker
  })
)

export const decorateRGB11AssetItems = (items: any[], state: RGB11StateDTO) => items.map((item: any) => {
  const name = {
    Protocol: item.protocol,
    Type: item.type,
    Ticker: item.ticker,
  }
  const tickerInfo = tickerInfoFor(state, name)
  // Balance keys contain the complete ContractID. Neither an editable SDK
  // label nor a registered display name may change selection/transfer identity.
  const assetKey = `${name.Protocol || 'rgb11'}:${name.Type || 'f'}:${name.Ticker || ''}`
  const canonicalName = String(tickerInfo?.canonical_name || tickerInfo?.CanonicalName || '')
  const contractId = String(tickerInfo?.contract_id || tickerInfo?.ContractID || '')
  const displayName = String(tickerInfo?.displayname || tickerInfo?.DisplayName || '').trim()
  const symbol = String(tickerInfo?.ticker || tickerInfo?.Ticker || '').trim()
  const verified = canonicalName !== '' && Boolean(tickerInfo?.verified ?? tickerInfo?.Verified ?? false)
  return {
    ...item,
    id: assetKey,
    key: assetKey,
    label: symbol || canonicalName || contractId || displayName || assetKey,
    symbol,
    asset_key: assetKey,
    canonical_name: canonicalName,
    contract_id: contractId,
    display_name: displayName,
    genesis_address: String(tickerInfo?.genesis_address || ''),
    naming_status: String(tickerInfo?.naming_status || 'origin-unavailable'),
    verified,
    precision: Number(item.precision ?? tickerInfo?.divisibility ?? 0),
  }
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
    queryFn: async (): Promise<RGB11StateDTO> => {
      const [err, result] = await walletManager.getRGB11State()
      if (err) throw err
      if (!result?.state) throw new Error('RGB11 Wallet state is unavailable')
      return JSON.parse(result.state) as RGB11StateDTO
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
