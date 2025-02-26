import { useQuery } from '@tanstack/vue-query'
import { ordxApi } from '@/apis'
import { parallel } from 'radash'
import { useAssetsStore, useWalletStore } from '@/store'

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

export const useAssets = () => {
  const assetsStore = useAssetsStore()
  const walletStore = useWalletStore()
  const { address, network } = storeToRefs(walletStore)
  console.log('address', address)
  console.log('network', network);
  
  const allAssetList = ref<AssetItem[]>([])

  // Queries
  const nsQuery = useQuery({
    queryKey: ['ns', address, network],
    queryFn: () => ordxApi.getNsListByAddress({ address: address.value, network: network.value }),
    enabled: computed(() => !!address.value && !!network.value),
  })

  const summaryQuery = useQuery({
    queryKey: ['summary', address, network],
    queryFn: () => ordxApi.getAddressSummary({ address: address.value, network: network.value }),
    enabled: computed(() => !!address.value && !!network.value),
  })

  // Asset Processing Functions
  const processAssetUtxo = async (key: string, start = 0, limit = 100) => {
    const result = await ordxApi.getOrdxAddressHolders({
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
    console.log('summaryQuery.data.value', summaryQuery.data.value);
    
    const assets = summaryQuery.data.value?.data || []
    assets.forEach((item: any) => {
      const key = item.Name.Protocol
        ? `${item.Name.Protocol}:${item.Name.Type}:${item.Name.Ticker}`
        : '::'

      if (!allAssetList.value.find((v) => v?.key === key)) {
        allAssetList.value.push({
          id: key,
          key,
          protocol: item.Name.Protocol,
          type: item.Name.Type,
          label: item.Name.Type === 'e' ? `${item.Name.Ticker}（raresats）` : item.Name.Ticker,
          ticker: item.Name.Ticker,
          utxos: [],
          amount: item.Amount,
        })
      }
    })
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
      ...(list.some((item) => item?.protocol === 'ordx') ? [{ label: 'SAT20', value: 'ordx' }] : []),
      ...(list.some((item) => item?.protocol === 'runes') ? [{ label: 'Runes', value: 'runes' }] : []),
    ]
    assetsStore.setUniqueAssetList(uniqueTypes)
    
  }

  // Watchers & Effects
  watch(() => summaryQuery.data.value, async (newData) => {
    if (newData) {
      console.log('newData', newData.data);
      
      await parseAssetSummary()
      console.log('allAssetList.value', allAssetList.value);
      
      processAllUtxos(allAssetList.value.map((item) => item.key))
      assetsStore.setAssetList(newData?.data || [])
    }
  })

  watch(allAssetList, updateStoreAssets, { deep: true })

  watch(address, () => {
    if (address.value && network.value) {
      summaryQuery.refetch()
      nsQuery.refetch()
    }
  })

  return {
    loading: computed(() => summaryQuery.isLoading.value || nsQuery.isLoading.value),
    retry: () => {
      summaryQuery.refetch()
      allAssetList.value = []
    },
  }
}
