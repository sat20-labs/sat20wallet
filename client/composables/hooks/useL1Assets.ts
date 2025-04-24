import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { ordxApi, satnetApi } from '@/apis'
import { parallel } from 'radash'
import satsnetStp from '@/utils/stp'
import { useL1Store, useWalletStore } from '@/store'
import { Chain } from '@/types'
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
  refreshNs?: boolean
  refreshSummary?: boolean
  refreshUtxos?: boolean
  clearCache?: boolean
}

export const useL1Assets = () => {
  const assetsStore = useL1Store()
  const walletStore = useWalletStore()
  const { address, network, chain } = storeToRefs(walletStore)
  const queryClient = useQueryClient()

  const allAssetList = ref<AssetItem[]>([])

  const clientApi = computed(() => {
    return ordxApi
  })

  const nsQuery = useQuery({
    queryKey: ['ns-l1', address, network],
    queryFn: () =>
      clientApi.value.getNsListByAddress({
        address: address.value,
        network: network.value,
      }),
    enabled: computed(() => !!address.value && !!network.value),
  })

  const summaryQuery = useQuery({
    queryKey: ['summary-l1', address, network],
    queryFn: () =>
      clientApi.value.getAddressSummary({
        address: address.value,
        network: network.value,
      }),
    enabled: computed(() => !!address.value && !!network.value),
  })

  // Asset Processing Functions
  const processAssetUtxo = async (key: string, start = 0, limit = 100) => {
    const result = await clientApi.value.getOrdxAddressHolders({
      address: address.value,
      ticker: key,
      network: network.value,
      start,
      limit,
    })

    if (result?.data?.length) {
      result.data.forEach(({ Outpoint }: any) => {
        const findItem = allAssetList.value?.find((a) => a.key === key)
        if (findItem && !findItem.utxos?.includes(Outpoint)) {
          findItem.utxos.push(Outpoint)
        }
      })
    }
  }

  const processAllUtxos = async (tickers: string[]) => {
    if (!tickers.length) return
    await parallel(3, tickers, (ticker) => processAssetUtxo(ticker))
  }

  const parseAssetSummary = async () => {
    console.log('summaryQuery.data.value', summaryQuery.data.value)

    const assets = summaryQuery.data.value?.data || []
    for await (const item of assets) {
      const key = item.Name.Protocol
        ? `${item.Name.Protocol}:${item.Name.Type}:${item.Name.Ticker}`
        : '::'

      if (!allAssetList.value.find((v) => v?.key === key)) {
        let label = item.Name.Type === 'e'
        ? `${item.Name.Ticker}（raresats）`
        : item.Name.Ticker;
        if (key !== '::') {
          const [err, res] = await satsnetStp.getTickerInfo(key)
          console.log('ticker res', res)
          if (res?.ticker) {
            const { ticker } = res
            const result = JSON.parse(ticker)
            console.log('ticker result', result)

            label = result?.displayname || label
          }
        }
        allAssetList.value.push({
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
  }

  // Store Updates
  const updateStoreAssets = (list: AssetItem[]) => {
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
  }

  // Watchers & Effects
  watch(
    () => summaryQuery.data.value,
    async (newData) => {
      console.log('newData', newData)

      if (newData) {
        allAssetList.value = []
        console.log('newData', newData.data)

        await parseAssetSummary()
        console.log('allAssetList.value', allAssetList.value)

        processAllUtxos(allAssetList.value.map((item) => item.key))
        assetsStore.setAssetList(newData?.data || [])
      }
    },
    {
      deep: true,
      immediate: true,
    }
  )

  watch(allAssetList, updateStoreAssets, { deep: true, immediate: true })

  /**
   * 刷新所有资产数据
   * @param {RefreshOptions} options - 刷新选项
   * @param {boolean} options.resetState - 是否重置状态，默认为 true
   * @param {boolean} options.refreshNs - 是否刷新命名空间数据，默认为 true
   * @param {boolean} options.refreshSummary - 是否刷新摘要数据，默认为 true
   * @param {boolean} options.refreshUtxos - 是否在摘要数据刷新后重新处理 UTXO，默认为 true
   * @param {boolean} options.clearCache - 是否清除缓存，默认为 true
   * @returns {Promise<void>}
   */
  const refreshL1Assets = async (options: RefreshOptions = {}) => {
    const {
      resetState = true,
      refreshNs = true,
      refreshSummary = true,
      refreshUtxos = true,
      clearCache = true,
    } = options

    // 清除缓存
    if (clearCache) {
      // 清除特定查询的缓存
      if (refreshNs) {
        queryClient.invalidateQueries({
          queryKey: ['ns-l1', address.value, network.value],
        })
      }
      if (refreshSummary) {
        queryClient.invalidateQueries({
          queryKey: ['summary-l1', address.value, network.value],
        })
      }

      // 可选：清除与当前地址相关的所有缓存
      // queryClient.invalidateQueries({ predicate: (query) => {
      //   const queryKey = query.queryKey as string[]
      //   return queryKey.includes(address.value)
      // }})
    }

    // 重置状态

    // 创建一个 Promise 数组来收集所有需要等待的请求
    const refreshPromises = []

    // 刷新命名空间数据
    if (refreshNs) {
      refreshPromises.push(nsQuery.refetch())
    }

    // 刷新摘要数据
    if (refreshSummary) {
      const summaryPromise = summaryQuery.refetch()
      refreshPromises.push(summaryPromise)
    }

    // 等待所有刷新操作完成
    await Promise.all(refreshPromises)
  }

  return {
    loading: computed(
      () => summaryQuery.isLoading.value || nsQuery.isLoading.value
    ),
    refreshL1Assets,
  }
}
