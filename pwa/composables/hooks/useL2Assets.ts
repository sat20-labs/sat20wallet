import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { ref, computed, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { satnetApi } from '@/apis'
import { useGlobalStore, useL2Store, useWalletStore } from '@/store'
import {
  applyAssetSnapshot,
  buildAssetSnapshotFromAssets,
  loadAssetSnapshot,
  peekAssetSnapshot,
  saveAssetSnapshot,
} from '@/lib/assetSnapshotStorage'
import { assetContextKey, isSameAssetContext, type AssetContext } from '@/lib/assetContext'
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

type AssetQueryContext = AssetContext & { chain: 'satnet' }

interface SummaryQueryResult {
  context: AssetQueryContext
  response: any
}

const l2RefreshPromises = new Map<string, Promise<void>>()

export const useL2Assets = (options: UseAssetQueryOptions = {}) => {
  const assetsStore = useL2Store()
  const walletStore = useWalletStore()
  const globalStore = useGlobalStore()
  const { address, network, chain, walletId, accountIndex } = storeToRefs(walletStore)
  const { env } = storeToRefs(globalStore)
  console.log('address.value', address.value)
  console.log('network.value', network.value)
  console.log('chain.value', chain.value)

  const queryClient = useQueryClient()

  const allAssetList = ref<AssetItem[]>([])
  let successfulResponseGeneration = 0

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
      env: env.value,
      network: network.value,
      chain: 'satnet',
      walletId: walletId.value,
      accountIndex: accountIndex.value,
      address: address.value,
    }
  }

  const isCurrentContext = (context: AssetQueryContext) => isSameAssetContext(context, currentContext())

  const summaryQuery = useQuery({
    queryKey: ['summary-l2', env, network, computed(() => 'satnet'), walletId, accountIndex, address],
    queryFn: async (): Promise<SummaryQueryResult | null> => {
      const context = currentContext()
      if (!context) return null
      const response = await clientApi.value.getAddressSummary({
        address: context.address,
        network: context.network,
      })
      const responseCode = Number(response?.code ?? response?.Code ?? -1)
      if (responseCode !== 0) {
        throw new Error(response?.msg || response?.Msg || `L2 asset summary failed with code ${responseCode}`)
      }
      if (!Array.isArray(response?.data)) {
        throw new Error('L2 asset summary returned malformed data')
      }
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

  const snapshotInput = computed(() => currentContext())

  const persistSnapshot = async (
    context: AssetQueryContext,
    rawAssets: any[],
    parsedAssets: AssetItem[],
    totalSats: number
  ) => {
    if (!isCurrentContext(context)) return
    await saveAssetSnapshot(
      context,
      buildAssetSnapshotFromAssets(
        rawAssets,
        parsedAssets,
        totalSats
      )
    )
  }

  const hydrateSnapshot = async (context: AssetQueryContext | null) => {
    if (!context) {
      allAssetList.value = []
      assetsStore.reset()
      return
    }
    const generation = successfulResponseGeneration
    const cached = peekAssetSnapshot(context)
    if (!cached && isCurrentContext(context)) {
      allAssetList.value = []
      assetsStore.reset()
    }
    const snapshot = cached || await loadAssetSnapshot(context)
    if (!isCurrentContext(context) || generation !== successfulResponseGeneration) return
    if (!snapshot) return
    applyAssetSnapshot(assetsStore, snapshot)
    allAssetList.value = [
      ...(snapshot.plainList || []),
      ...(snapshot.sat20List || []),
      ...(snapshot.runesList || []),
      ...(snapshot.brc20List || []),
      ...(snapshot.ordList || []),
    ]
  }

  // Watchers & Effects
  watch(snapshotInput, hydrateSnapshot, { immediate: true })

  watch(
    () => summaryQuery.data.value,
    async (payload) => {
      if (!payload?.context || !payload.response || !isCurrentContext(payload.context)) return

      successfulResponseGeneration += 1
      const rawAssets = payload.response.data
      const { list, totalSats } = parseAssetSummary(rawAssets)
      allAssetList.value = list
      updateStoreAssets(list, totalSats)
      assetsStore.setAssetList(rawAssets)
      await persistSnapshot(payload.context, rawAssets, list, totalSats)
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
    const context = currentContext()
    if (!context) return
    const refreshKey = assetContextKey(context)
    const existing = l2RefreshPromises.get(refreshKey)
    if (existing) return existing

    const refreshPromise = (async () => {
      const {
        resetState = true,
        refreshSummary = true,
        clearCache = true,
      } = options

      if (clearCache && refreshSummary) {
        queryClient.invalidateQueries({ queryKey: ['summary-l2'] })
      }
      if (resetState) {
        await hydrateSnapshot(context)
      }
      const refreshPromises = []

      if (queryEnabled.value && refreshSummary) {
        refreshPromises.push(summaryQuery.refetch())
      }

      await Promise.all(refreshPromises)
    })().finally(() => {
      l2RefreshPromises.delete(refreshKey)
    })

    l2RefreshPromises.set(refreshKey, refreshPromise)
    return refreshPromise
  }

  return {
    loading: computed(() => summaryQuery.isLoading.value),
    refreshL2Assets,
  }
}
