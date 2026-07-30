import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { ref, computed, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { ordxApi } from '@/apis'
import walletManager from '@/utils/sat20'
import { useGlobalStore, useL1Store, useRGB11Store, useWalletStore } from '@/store'
import { decorateRGB11AssetItems } from './useRgb11Assets'
import {
  applyAssetSnapshot,
  buildAssetSnapshotFromAssets,
  loadAssetSnapshot,
  saveAssetSnapshot,
} from '@/lib/assetSnapshotStorage'
interface AssetItem {
  id: string
  key: string
  protocol: string
  type: string
  label: string
  ticker: string
  utxos: string[]
  amount: number | string
  precision: number
  available_amount?: string
  pending_amount?: string
}

// 定义刷新选项接口
interface RefreshOptions {
  resetState?: boolean
  refreshNs?: boolean
  refreshSummary?: boolean
  clearCache?: boolean
}

interface UseAssetQueryOptions {
  enabled?: boolean | { value: boolean }
  beforeSummaryFetch?: () => Promise<void>
}

interface AssetQueryContext {
  env: string
  network: string
  chain: 'btc'
  walletId: string
  accountIndex: number
  address: string
}

interface SummaryQueryResult {
  context: AssetQueryContext
  response: any
}

const l1RefreshPromises = new Map<string, Promise<void>>()

const decimalText = (amount: any, precisionHint = 0): string => {
  if (typeof amount === 'string' || typeof amount === 'number') {
    return String(amount)
  }
  const value = String(amount?.Value ?? amount?.value ?? '0')
  const precision = Number(amount?.Precision ?? amount?.precision ?? precisionHint)
  if (!precision) return value
  const negative = value.startsWith('-')
  const digits = negative ? value.slice(1) : value
  const padded = digits.padStart(precision + 1, '0')
  const split = padded.length - precision
  const text = `${padded.slice(0, split)}.${padded.slice(split)}`.replace(/\.?0+$/, '')
  return negative ? `-${text}` : text
}

export const useL1Assets = (options: UseAssetQueryOptions = {}) => {
  const assetsStore = useL1Store()
  const rgb11Store = useRGB11Store()
  const walletStore = useWalletStore()
  const globalStore = useGlobalStore()
  const { address, network, walletId, accountIndex } = storeToRefs(walletStore)
  const { env } = storeToRefs(globalStore)
  const queryClient = useQueryClient()

  const allAssetList = ref<AssetItem[]>([])
  const hydratingSnapshot = ref(false)

  const clientApi = computed(() => {
    return ordxApi
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
      chain: 'btc',
      walletId: walletId.value,
      accountIndex: accountIndex.value,
      address: address.value,
    }
  }

  const isCurrentContext = (context: AssetQueryContext) => (
    context.env === env.value &&
    context.network === network.value &&
    context.walletId === walletId.value &&
    context.accountIndex === accountIndex.value &&
    context.address === address.value
  )

  const nsQuery = useQuery({
    queryKey: ['ns-l1', address, network, env],
    queryFn: () => {
      const context = currentContext()
      if (!context) return null
      return clientApi.value.getNsListByAddress({
        address: context.address,
        network: context.network,
      })
    },
    refetchInterval: computed(() => queryEnabled.value ? 10 * 60 * 1000 : false),
    enabled: computed(() => queryEnabled.value && !!address.value && !!network.value),
  })

  const summaryQuery = useQuery({
    queryKey: ['summary-l1', address, network, env, walletId, accountIndex],
    queryFn: async (): Promise<SummaryQueryResult | null> => {
      const context = currentContext()
      if (!context) return null
      if (options.beforeSummaryFetch) {
        try {
          await options.beforeSummaryFetch()
        } catch (error) {
          console.warn('RGB11 background sync failed; using validated local state:', error)
        }
      }
      const [error, result] = await walletManager.getAssetSummary(context.address)
      if (error) throw error
      return { context, response: { data: result?.assets || [] } }
    },
    enabled: computed(() => queryEnabled.value && !!address.value && !!network.value),
  })

  const parseAssetSummary = (assets: any[]) => {
    const list: AssetItem[] = []
    let totalSats = 0
    for (const item of assets) {
      const precision = Number(item.Precision ?? item.Amount?.Precision ?? 0)
      const amountText = decimalText(item.Amount, precision)
      const key = item.Name.Protocol
        ? `${item.Name.Protocol}:${item.Name.Type}:${item.Name.Ticker}`
        : '::'
      if (item.Name.Type === '*') {
        totalSats = Number(amountText)
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
          amount: item.Name.Protocol === 'rgb11' ? amountText : Number(amountText),
          precision,
        })
      }
    }
    return { list, totalSats }
  }

  const rgb11StateAssets = () => {
    const total = parseAssetSummary(rgb11Store.state.assets || []).list
    const available = new Map(
      parseAssetSummary(rgb11Store.state.available_assets || []).list.map((item) => [item.key, String(item.amount)])
    )
    const pending = new Map(
      parseAssetSummary(rgb11Store.state.pending_assets || []).list.map((item) => [item.key, String(item.amount)])
    )
    return total.map((item) => ({
      ...item,
      available_amount: available.get(item.key) || '0',
      pending_amount: pending.get(item.key) || '0',
    }))
  }

  const presentAssets = (list: AssetItem[]) => {
    const stateRGB11 = rgb11StateAssets()
    const rgb11 = stateRGB11.length
      ? stateRGB11
      : list.filter((item) => item.protocol === 'rgb11').map((item) => ({
          ...item,
          available_amount: String(item.amount),
          pending_amount: '0',
        }))
    return [
      ...list.filter((item) => item.protocol !== 'rgb11'),
      ...decorateRGB11AssetItems(rgb11, rgb11Store.state),
    ]
  }

  // Store Updates
  const updateStoreAssets = (list: AssetItem[], totalSats: number) => {
    assetsStore.setSat20List(list.filter((item) => item?.protocol === 'ordx'))
    assetsStore.setRunesList(list.filter((item) => item?.protocol === 'runes'))
    assetsStore.setBrc20List(list.filter((item) => item?.protocol === 'brc20'))
    assetsStore.setOrdList(list.filter((item) => item?.protocol === 'ord'))
    const rgb11 = decorateRGB11AssetItems(
      list.filter((item) => item?.protocol === 'rgb11'),
      rgb11Store.state
    )
    assetsStore.setRGB11List(rgb11)

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
      ...(rgb11.length ? [{ label: 'RGB11', value: 'rgb11' }] : []),
    ]
    assetsStore.setUniqueAssetList(uniqueTypes)
    assetsStore.setTotalSats(totalSats)
  }

  const snapshotInput = computed(() => queryEnabled.value ? currentContext() : null)

  const persistSnapshot = async (
    context: AssetQueryContext,
    rawAssets: any[],
    parsedAssets: AssetItem[],
    totalSats: number
  ) => {
    if (hydratingSnapshot.value || !isCurrentContext(context)) return
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
    if (!context) return
    hydratingSnapshot.value = true
    try {
      const snapshot = await loadAssetSnapshot(context)
      if (snapshot && isCurrentContext(context)) {
        applyAssetSnapshot(assetsStore, snapshot)
        allAssetList.value = [
          ...(snapshot.plainList || []),
          ...(snapshot.sat20List || []),
          ...(snapshot.runesList || []),
          ...(snapshot.brc20List || []),
          ...(snapshot.ordList || []),
          ...(snapshot.rgb11List || []),
        ]
      }
    } finally {
      hydratingSnapshot.value = false
    }
  }

  // Watchers & Effects
  watch(snapshotInput, hydrateSnapshot, { immediate: true })
  watch(
    () => rgb11Store.state,
    () => {
      const presented = presentAssets(allAssetList.value)
      allAssetList.value = presented
      updateStoreAssets(presented, assetsStore.totalSats)
    },
    { deep: true }
  )

  watch(
    () => summaryQuery.data.value,
    async (payload) => {
      if (!payload?.context || !payload.response || !isCurrentContext(payload.context)) return

      const rawAssets = payload.response?.data || []
      const { list, totalSats } = parseAssetSummary(rawAssets)
      const presented = presentAssets(list)
      allAssetList.value = presented
      updateStoreAssets(presented, totalSats)
      assetsStore.setAssetList(rawAssets)
      await persistSnapshot(payload.context, rawAssets, presented, totalSats)
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
   * @param {boolean} options.refreshNs - 是否刷新命名空间数据，默认为 true
   * @param {boolean} options.refreshSummary - 是否刷新摘要数据，默认为 true
   * @param {boolean} options.clearCache - 是否清除缓存，默认为 true
   * @returns {Promise<void>}
   */
  const refreshL1Assets = async (options: RefreshOptions = {}) => {
    const context = currentContext()
    if (!context) return
    const refreshKey = `${context.env}:${context.network}:${context.walletId}:${context.accountIndex}:${context.address}`
    const existing = l1RefreshPromises.get(refreshKey)
    if (existing) return existing

    const refreshPromise = (async () => {
      const {
        resetState = true,
        refreshNs = true,
        refreshSummary = true,
        clearCache = true,
      } = options

      if (clearCache) {
        if (refreshNs) {
          queryClient.invalidateQueries({ queryKey: ['ns-l1'] })
        }
        if (refreshSummary) {
          queryClient.invalidateQueries({ queryKey: ['summary-l1'] })
        }
      }

      if (resetState) {
        allAssetList.value = []
        assetsStore.reset()
      }

      const refreshPromises = []

      if (queryEnabled.value && refreshNs) {
        refreshPromises.push(nsQuery.refetch())
      }

      if (queryEnabled.value && refreshSummary) {
        refreshPromises.push(summaryQuery.refetch())
      }

      await Promise.all(refreshPromises)
    })().finally(() => {
      l1RefreshPromises.delete(refreshKey)
    })

    l1RefreshPromises.set(refreshKey, refreshPromise)
    return refreshPromise
  }

  return {
    loading: computed(
      () => summaryQuery.isLoading.value || nsQuery.isLoading.value
    ),
    refreshL1Assets,
  }
}
