import { defineStore } from 'pinia'
import { ref, computed } from 'vue'

export const useL1Store = defineStore('l1-assets', () => {
  const assetList = ref<any[]>([])
  const uniqueAssetList = ref<any[]>([])
  const sat20List = ref<any[]>([])
  const plainList = ref<any[]>([])
  const plainUtxos = ref<any[]>([])
  const runesList = ref<any[]>([])
  const brc20List = ref<any[]>([])
  const ordList = ref<any[]>([])
  const totalSats = ref<number>(0)
  const setAssetList = (list: any[]) => (assetList.value = list)
  const setUniqueAssetList = (list: any[]) => (uniqueAssetList.value = list)
  const setSat20List = (list: any[]) => (sat20List.value = list)
  const setPlainList = (list: any[]) => (plainList.value = list)
  const setPlainUtxos = (list: any[]) => (plainUtxos.value = list)
  const setRunesList = (list: any[]) => (runesList.value = list)
  const setBrc20List = (list: any[]) => (brc20List.value = list)
  const setOrdList = (list: any[]) => (ordList.value = list)
  const setTotalSats = (total: number) => (totalSats.value = total)

  const balance = computed(() =>
    plainList.value?.reduce((acc, item) => acc + Number(item.amount), 0)
  )
  const reset = () => {
    assetList.value = []
    uniqueAssetList.value = []
    sat20List.value = []
    plainList.value = []
    plainUtxos.value = []
    runesList.value = []
    brc20List.value = []
    ordList.value = []
  }
  return {
    assetList,
    uniqueAssetList,
    sat20List,
    plainList,
    plainUtxos,
    runesList,
    brc20List,
    ordList,  
    totalSats,
    setTotalSats,
    setAssetList,
    setUniqueAssetList,
    setSat20List,
    setPlainList,
    setPlainUtxos,
    setRunesList,
    setBrc20List,
    setOrdList,
    balance,
    reset,
  }
})
