import { Storage } from './storage-adapter'

export interface AssetSnapshot {
  assetList: any[]
  uniqueAssetList: any[]
  sat20List: any[]
  plainList: any[]
  plainUtxos: any[]
  runesList: any[]
  brc20List: any[]
  ordList: any[]
  totalSats: number
  updatedAt: number
}

interface SnapshotKeyInput {
  env: string
  network: string
  chain: string
  address: string
}

const snapshotKey = ({ env, network, chain, address }: SnapshotKeyInput) =>
  `local:wallet_asset_snapshot:${env}:${network}:${chain}:${address}`

export const loadAssetSnapshot = async (
  input: SnapshotKeyInput
): Promise<AssetSnapshot | null> => {
  const { value } = await Storage.get({ key: snapshotKey(input) })
  if (!value) return null

  try {
    return JSON.parse(value) as AssetSnapshot
  } catch (error) {
    console.warn('Failed to parse asset snapshot:', error)
    return null
  }
}

export const saveAssetSnapshot = async (
  input: SnapshotKeyInput,
  snapshot: Omit<AssetSnapshot, 'updatedAt'> & { updatedAt?: number }
) => {
  await Storage.set({
    key: snapshotKey(input),
    value: JSON.stringify({
      ...snapshot,
      updatedAt: snapshot.updatedAt || Date.now(),
    }),
  })
}

export const buildAssetSnapshotFromAssets = (
  assetList: any[],
  parsedAssets: any[],
  totalSats: number
): AssetSnapshot => {
  const list = parsedAssets || []
  const plainList = list.filter((item) => item?.protocol === '')
  const sat20List = list.filter((item) => item?.protocol === 'ordx')
  const runesList = list.filter((item) => item?.protocol === 'runes')
  const brc20List = list.filter((item) => item?.protocol === 'brc20')

  return {
    assetList: assetList || [],
    uniqueAssetList: [
      ...(plainList.length ? [{ label: 'Btc', value: 'btc' }] : []),
      ...(sat20List.length ? [{ label: 'ORDX', value: 'ordx' }] : []),
      ...(runesList.length ? [{ label: 'Runes', value: 'runes' }] : []),
    ],
    sat20List,
    plainList,
    plainUtxos: plainList?.[0]?.utxos || [],
    runesList,
    brc20List,
    ordList: list.filter((item) => item?.protocol === 'ord'),
    totalSats: Number(totalSats || 0),
    updatedAt: Date.now(),
  }
}

export const applyAssetSnapshot = (
  store: {
    setAssetList: (list: any[]) => void
    setUniqueAssetList: (list: any[]) => void
    setSat20List: (list: any[]) => void
    setPlainList: (list: any[]) => void
    setPlainUtxos: (list: any[]) => void
    setRunesList: (list: any[]) => void
    setBrc20List: (list: any[]) => void
    setOrdList: (list: any[]) => void
    setTotalSats: (total: number) => void
  },
  snapshot: AssetSnapshot
) => {
  store.setAssetList(snapshot.assetList || [])
  store.setUniqueAssetList(snapshot.uniqueAssetList || [])
  store.setSat20List(snapshot.sat20List || [])
  store.setPlainList(snapshot.plainList || [])
  store.setPlainUtxos(snapshot.plainUtxos || [])
  store.setRunesList(snapshot.runesList || [])
  store.setBrc20List(snapshot.brc20List || [])
  store.setOrdList(snapshot.ordList || [])
  store.setTotalSats(Number(snapshot.totalSats || 0))
}
