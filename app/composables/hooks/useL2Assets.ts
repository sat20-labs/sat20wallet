import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { ref, computed, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { ordxApi, satnetApi } from '@/apis'
import { parallel } from 'radash'
import { useL2Store, useWalletStore } from '@/store'
import satsnetStp from '@/utils/stp'
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

export const useL2Assets = () => {
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

  const summaryQuery = useQuery({
    queryKey: ['summary-l2', address, network],
    queryFn: () =>
      clientApi.value.getAddressSummary({
        address: address.value,
        network: network.value,
      }),
    refetchInterval: 3000,
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
      if (item.Name.Type === '*') {
        const totalSats = item.Amount
        assetsStore.setTotalSats(totalSats)
      }
      if (!allAssetList.value.find((v) => v?.key === key)) {
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
      console.log('newData address', address.value)
      console.log('newData network', network.value)
      console.log('newData chain', chain.value)
      console.log('newData', newData)
      allAssetList.value = []
      assetsStore.setTotalSats(0)
      if (newData) {
        console.log('newData', newData.data)
        console.log('allAssetList.value', allAssetList.value)
        await parseAssetSummary()

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
  const refreshL2Assets = async (options: RefreshOptions = {}) => {
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
      if (refreshSummary) {
        queryClient.invalidateQueries({
          queryKey: ['summary-l2', address.value, network.value],
        })
      }

      // 可选：清除与当前地址相关的所有缓存
      queryClient.invalidateQueries({
        predicate: (query) => {
          const queryKey = query.queryKey as string[]
          return queryKey.includes(address.value || '')
        },
      })
    }
    const refreshPromises = []

    // 刷新摘要数据
    if (refreshSummary) {
      const summaryPromise = summaryQuery.refetch()
      refreshPromises.push(summaryPromise)
    }

    // 等待所有刷新操作完成
    await Promise.all(refreshPromises)
  }

  return {
    loading: computed(() => summaryQuery.isLoading.value),
    refreshL2Assets,
  }
}
