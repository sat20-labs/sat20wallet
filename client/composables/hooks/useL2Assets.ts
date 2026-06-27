import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { ordxApi, satnetApi } from '@/apis'
import { useL2Store, useWalletStore } from '@/store'
interface AssetItem {
  id: string
  key: string
  protocol: string
  type: string
  label: string
  ticker: string
  utxos: string[]
  amount: number
}

// 定义刷新选项接口
interface RefreshOptions {
  resetState?: boolean
  refreshSummary?: boolean
  clearCache?: boolean
}

interface UseAssetQueryOptions {
  enabled?: boolean | { value: boolean }
}

interface AssetQueryContext {
  network: string
  chain: 'satnet'
  address: string
}

interface SummaryQueryResult {
  context: AssetQueryContext
  response: any
}

let l2RefreshPromise: Promise<void> | null = null

export const useL2Assets = (options: UseAssetQueryOptions = {}) => {
  const assetsStore = useL2Store()
  const walletStore = useWalletStore()
  const { address, network, chain } = storeToRefs(walletStore)
  console.log('address.value', address.value)
  console.log('network.value', network.value)
  console.log('chain.value', chain.value)

  const queryClient = useQueryClient()

  const allAssetList = ref<AssetItem[]>([])

  const clientApi = computed(() => {
    return satnetApi
  })

  const queryEnabled = computed(() => {
    const enabled = options.enabled
    if (typeof enabled === 'boolean') return enabled
    return enabled?.value ?? true
  })

  const currentContext = (): AssetQueryContext | null => {
    if (!address.value || !network.value) return null
    return {
      network: network.value,
      chain: 'satnet',
      address: address.value,
    }
  }

  const isCurrentContext = (context: AssetQueryContext) => (
    context.network === network.value &&
    context.address === address.value
  )

  const summaryQuery = useQuery({
    queryKey: ['summary-l2', address, network],
    queryFn: async (): Promise<SummaryQueryResult | null> => {
      const context = currentContext()
      if (!context) return null
      const response = await clientApi.value.getAddressSummary({
        address: context.address,
        network: context.network,
      })
      return { context, response }
    },
    refetchInterval: computed(() => queryEnabled.value ? 60 * 1000 : false),
    enabled: computed(() => queryEnabled.value && !!address.value && !!network.value),
  })

  const parseAssetSummary = (assets: any[]) => {
    const list: AssetItem[] = []
    let totalSats = 0
    for (const item of assets) {
      const key = item.Name.Protocol
        ? `${item.Name.Protocol}:${item.Name.Type}:${item.Name.Ticker}`
        : '::'
      if (item.Name.Type === '*') {
        totalSats = item.Amount
      }
      if (!list.find((v) => v?.key === key)) {
        let label = item.Name.Type === 'e'
          ? `${item.Name.Ticker}（raresats）`
          : item.Name.Ticker;
        if (item.Name.Type === 'n') {
          continue
        }
        // if (key !== '::') {
        //   const [err, res] = await satsnetStp.getTickerInfo(key)

        //   if (res?.ticker) {
        //     const { ticker } = res
        //     const result = JSON.parse(ticker)
        //     console.log('l2 ticker result', result)
        //     label = result?.name.Ticker || label
        //   }
        // }
        list.push({
          id: key,
          key,
          protocol: item.Name.Protocol,
          type: item.Name.Type,
          label: label,
          ticker: item.Name.Ticker,
          utxos: [],
          amount: item.Amount,
        })
      }
    }
    return { list, totalSats }
  }

  // Store Updates
  const updateStoreAssets = (list: AssetItem[], totalSats: number) => {
    assetsStore.setSat20List(list.filter((item) => item?.protocol === 'ordx'))
    assetsStore.setRunesList(list.filter((item) => item?.protocol === 'runes'))
    assetsStore.setBrc20List(list.filter((item) => item?.protocol === 'brc20'))
    assetsStore.setOrdList(list.filter((item) => item?.protocol === 'ord'))

    const plain = list.filter((item) => item?.protocol === '')
    assetsStore.setPlainList(plain)
    assetsStore.setPlainUtxos(plain?.[0]?.utxos || [])

    const uniqueTypes = [
      ...(plain?.length ? [{ label: 'Btc', value: 'btc' }] : []),
      ...(list.some((item) => item?.protocol === 'ordx')
        ? [{ label: 'ORDX', value: 'ordx' }]
        : []),
      ...(list.some((item) => item?.protocol === 'runes')
        ? [{ label: 'Runes', value: 'runes' }]
        : []),
    ]
    assetsStore.setUniqueAssetList(uniqueTypes)
    assetsStore.setTotalSats(totalSats)
  }

  // Watchers & Effects
  watch(
    () => summaryQuery.data.value,
    async (payload) => {
      if (!payload?.context || !payload.response || !isCurrentContext(payload.context)) return
      const rawAssets = payload.response?.data || []
      const { list, totalSats } = parseAssetSummary(rawAssets)
      allAssetList.value = list
      updateStoreAssets(list, totalSats)
      assetsStore.setAssetList(rawAssets)
    },
    {
      deep: true,
      immediate: true,
    }
  )

  /**
   * 刷新所有资产数据
   * @param {RefreshOptions} options - 刷新选项
   * @param {boolean} options.resetState - 是否重置状态，默认为 true
   * @param {boolean} options.refreshSummary - 是否刷新摘要数据，默认为 true
   * @param {boolean} options.clearCache - 是否清除缓存，默认为 true
   * @returns {Promise<void>}
   */
  const refreshL2Assets = async (options: RefreshOptions = {}) => {
    if (l2RefreshPromise) return l2RefreshPromise

    l2RefreshPromise = (async () => {
      const {
        resetState = true,
        refreshSummary = true,
        clearCache = true,
      } = options

      if (clearCache && refreshSummary) {
        queryClient.invalidateQueries({ queryKey: ['summary-l2'] })
      }
      if (resetState) {
        allAssetList.value = []
        assetsStore.reset()
      }
      const refreshPromises = []

      if (queryEnabled.value && refreshSummary) {
        refreshPromises.push(summaryQuery.refetch())
      }

      await Promise.all(refreshPromises)
    })().finally(() => {
      l2RefreshPromise = null
    })

    return l2RefreshPromise
  }

  return {
    loading: computed(() => summaryQuery.isLoading.value),
    refreshL2Assets,
  }
}
