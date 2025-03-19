import satsnetStp from '@/utils/stp'
import { parallel } from 'radash'
interface OutPoint {
  hash: string
  index: number
}

interface OutValue {
  value: number
  pkScript: string
  assets: any | null
}

interface Sat {
  start: number
  size: number
}

interface FundingUtxo {
  outPoint: OutPoint
  outValue: OutValue
  sats: Sat[]
  assets: any
}

interface LocalChanCfg {
  initialBalance: number
  paymentKey: object
  revocationBasePoint: object
}

interface RemoteChanCfg {
  initialBalance: number
  paymentKey: object
  revocationBasePoint: object
}

interface TxIn {
  previousOutPoint: OutPoint
  signatureScript: string | null
  witness: (string | null)[]
  sequence: number
}

interface TxOut {
  value: number
  pkScript: string
}

interface CommitTx {
  version: number
  txIn: TxIn[]
  txOut: TxOut[]
  lockTime: number
}

interface Commitment {
  version: number
  txIn: TxIn[]
  txOut: TxOut[]
  lockTime: number
}

interface Channel {
  version: number
  chanid: string
  shortchanid: number
  channelId: string
  initiator: boolean
  fundingutxos: FundingUtxo[]
  address: string
  status: number
  csvdelay: number
  peer: string
  capacity: number
  localbalance_L1: any[]
  remotebalance_L1: any[]
  commitheight: number
  lastpaymentid: string
  totalsent: number
  totalrecv: number
  localutxo_L2: any
  localunhandledutxos_L2: any[]
  remoteutxo_L2: any
  remoteunhandledutxos_L2: any[]
  localcommitment: Commitment
  remotecommitment: Commitment
  localDeAnchorTx: CommitTx
  remoteDeAnchorTx: CommitTx
}
export const useChannelStore = defineStore('channel', () => {
  
  const channels = ref<Channel[]>([])
  // const sat20List = ref<any[]>([])
  // const plainList = ref<any[]>([])
  const allAssetList = ref<any[]>([])
  const plainBalance = ref(0)

  const channel = computed(() => channels.value?.[0])
  const getAllChannels = async () => {
    const [_, result] = await satsnetStp.getAllChannels()
    if (result) {
      try {
        console.log('result', result)
        const c = JSON.parse(result.channels)
        console.log('channels', c)
        let values = Object.values(c)
        values = values.filter(
          (v: any) =>
            (v.status > 15 && v.status < 257) || (v.status > 0 && v.status < 5)
        )
        channels.value = values as Channel[]
      } catch (error) {
        console.log(error)
      }
    }
  }

  const parseChannel = async () => {
    allAssetList.value = []
    const { localbalance_L1 } = channel.value || {}
    if (localbalance_L1?.length) {
      for (let i = 0; i < localbalance_L1.length; i++) {
        const item = localbalance_L1[i]
        const protocol = item.Name.Protocol
        const key = protocol
          ? `${protocol}:${item.Name.Type}:${item.Name.Ticker}`
          : '::'
        let amt = item.Amount
        if (protocol === 'runes') {
          const [_, amtRes] = await satsnetStp.runesAmtV2ToV3(key, amt)
          amt = amtRes?.runeAmtInV3 || amt
        }
        allAssetList.value.push({
          id: key,
          key,
          protocol: protocol,
          type: item.Name.Type,
          ticker: item.Name.Ticker,
          label:
            item.Name.Type === 'e'
              ? `${item.Name.Ticker}（raresats）`
              : item.Name.Ticker,
          utxos: [],
          amount: amt,
        })
        const getAssetInfo = async (key: string) => {
          const [err, res] = await satsnetStp.getTickerInfo(key)
          if (res?.ticker) {
            const { ticker } = res
            const result = JSON.parse(ticker)
            const findItem = allAssetList.value?.find((a: any) => a.key === key)
            if (findItem) {
              findItem.label = result?.displayname || findItem.label
            }
          }
        }
        const tickers = allAssetList.value.map((a) => a.key)
        parallel(3, tickers.filter((r) => r !== '::') || [], async (ticker) => {
          return await getAssetInfo(ticker)
        })
      }
    }
  }
  const plainList = computed(() => {
    return allAssetList.value.filter((item) => item?.protocol === '')
  })
  const sat20List = computed(() => {
    return allAssetList.value.filter((item) => item?.protocol === 'ordx')
  })
  const runesList = computed(() => {
    return allAssetList.value.filter((item) => item?.protocol === 'runes')
  })
  const brc20List = computed(() => {
    return allAssetList.value.filter((item) => item?.protocol === 'brc20')
  })
  const ordList = computed(() => {
    return allAssetList.value.filter((item) => item?.protocol === 'ord')
  })
  const uniqueAssetList = computed(() => {
    const _assetTypes = []
    if (plainList.value?.length) {
      _assetTypes.push({
        label: 'Btc',
        value: 'btc',
      })
    }
    if (sat20List.value?.length) {
      _assetTypes.push({
        label: 'SAT20',
        value: 'ordx',
      })
    }
    if (runesList.value?.length) {
      _assetTypes.push({
        label: 'RUNES',
        value: 'runes',
      })
    }
    return _assetTypes
  })

  watch(
    channel,
    () => {
      parseChannel()
    },
    {
      immediate: true,
      deep: true,
    }
  )

  return {
    uniqueAssetList,
    sat20List,
    runesList,
    brc20List,
    ordList,
    plainList,
    plainBalance,
    channels,
    channel,
    getAllChannels,
  }
})
