import { computed, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { storeToRefs } from 'pinia'
import walletManager from '@/utils/sat20'
import { useGlobalStore, useL1Store, useRGB11Store, useWalletStore } from '@/store'
import type { RGB11StateDTO } from '@/store/rgb11'

interface UseAssetQueryOptions {
  enabled?: boolean | { value: boolean }
}

const decimalText = (amount: any): string => {
  const value = String(amount?.Value ?? amount?.value ?? '0')
  const precision = Number(amount?.Precision ?? amount?.precision ?? 0)
  if (!precision) return value
  const negative = value.startsWith('-')
  const digits = negative ? value.slice(1) : value
  const padded = digits.padStart(precision + 1, '0')
  const split = padded.length - precision
  const text = `${padded.slice(0, split)}.${padded.slice(split)}`.replace(/\.?0+$/, '')
  return negative ? `-${text}` : text
}

const outputHasAsset = (output: any, name: any) => (
  (output?.Assets || []).some((asset: any) => (
    asset?.Name?.Protocol === name?.Protocol &&
    asset?.Name?.Type === name?.Type &&
    asset?.Name?.Ticker === name?.Ticker
  ))
)

const assetNameOf = (value: any) => value?.Name || value?.name || value?.AssetName || {}

const tickerInfoFor = (state: RGB11StateDTO, name: any) => (
  (state.ticker_infos || []).find((info: any) => {
    const infoName = assetNameOf(info)
    return infoName?.Protocol === name?.Protocol &&
      infoName?.Type === name?.Type &&
      infoName?.Ticker === name?.Ticker
  })
)

const officialContractID = (ticker: unknown) => {
  const value = String(ticker || '')
  return value.startsWith('rgb:') ? value : `rgb:${value}`
}

const toAssetItems = (state: RGB11StateDTO) => (state.assets || []).map((asset: any) => {
  const name = asset?.Name || {}
  const tickerInfo = tickerInfoFor(state, name)
  const contractId = officialContractID(name.Ticker)
  const displayName = String(tickerInfo?.displayname || tickerInfo?.DisplayName || '').trim()
  const symbol = String(tickerInfo?.ticker || tickerInfo?.Ticker || '').trim()
  const key = `rgb11:${name.Type || 'f'}:${name.Ticker || ''}`
  return {
    id: contractId,
    key,
    protocol: 'rgb11',
    type: name.Type || 'f',
    label: symbol || displayName || contractId,
    symbol,
    ticker: name.Ticker || '',
    contract_id: contractId,
    display_name: displayName,
    utxos: (state.outputs || [])
      .filter((output: any) => outputHasAsset(output, name))
      .map((output: any) => output.OutPointStr),
    amount: decimalText(asset?.Amount),
    precision: Number(asset?.Amount?.Precision ?? asset?.Amount?.precision ?? tickerInfo?.divisibility ?? 0),
  }
})

export const useRgb11Assets = (options: UseAssetQueryOptions = {}) => {
  const walletStore = useWalletStore()
  const globalStore = useGlobalStore()
  const l1Store = useL1Store()
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
      // Lifecycle state is reconciled from Bitcoin facts before rendering;
      // failures remain visible through consistency_status.
      await walletManager.refreshRGB11State()
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
      const items = toAssetItems(state)
      l1Store.setRGB11List(items)
      l1Store.setAssetList([
        ...(l1Store.assetList || []).filter((asset: any) => asset?.Name?.Protocol !== 'rgb11'),
        ...(state.assets || []),
      ])
      const withoutRGB11 = (l1Store.uniqueAssetList || []).filter((item: any) => item?.value !== 'rgb11')
      l1Store.setUniqueAssetList([
        ...withoutRGB11,
        ...(items.length ? [{ label: 'RGB11', value: 'rgb11' }] : []),
      ])
    },
    { deep: true, immediate: true }
  )

  return {
    loading: computed(() => stateQuery.isLoading.value),
    refreshRGB11Assets: async () => { await stateQuery.refetch() },
  }
}
