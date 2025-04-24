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
  localbalanceL1: any[]
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
    const [_, resull] = await satsnetStp.getAllChannels()
    if (resull?.channels) {
      try {
        console.log('result', resull)
        const c = JSON.parse(resull.channels)
        console.log('channels', c)
        if (c && typeof c === 'object') {
          let values = Object.values(c)
          values = values.filter(
            (v: any) =>
              (v.status > 15 && v.status < 257) ||
              (v.status > 0 && v.status < 5)
          )
          channels.value = values as Channel[]
          // 添加这行，确保在通道数据更新后解析资产
          await parseChannel()
        } else {
          channels.value = []
        }
      } catch (error) {
        console.log(error)
        channels.value = []
      }
    } else {
      channels.value = []
    }
  }

  const parseChannel = async () => {
    console.log('开始解析通道资产...')
    console.log('当前channel:', channel.value)

    allAssetList.value = []
    const { localbalanceL1 } = channel.value || {}
    console.log('localbalanceL1:', localbalanceL1)

    if (localbalanceL1?.length) {
      for (let i = 0; i < localbalanceL1.length; i++) {
        const item = localbalanceL1[i]

        const protocol = item.Name.Protocol
        const key = protocol
          ? `${protocol}:${item.Name.Type}:${item.Name.Ticker}`
          : '::'

        const amt = item.Amount.Value
        const assetItem = {
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
        }
        console.log('添加资产项:', assetItem)
        console.log('allAssetList', allAssetList.value)
        console.log('l1', localbalanceL1)
        allAssetList.value.push(assetItem)
      }
      const getAssetInfo = async (key: string) => {
        console.log('获取资产信息:', key)
        const [err, res] = await satsnetStp.getTickerInfo(key)
        if (res?.ticker) {
          const { ticker } = res
          const result = JSON.parse(ticker)
          const findItem = allAssetList.value?.find((a: any) => a.key === key)
          if (findItem) {
            findItem.label = result?.displayname || findItem.label
            console.log('更新资产标签:', findItem)
          }
        }
      }

      const tickers = allAssetList.value.map((a) => a.key)
      console.log('待处理的ticker列表:', tickers)
      await parallel(
        3,
        tickers.filter((r) => r !== '::') || [],
        async (ticker) => {
          return await getAssetInfo(ticker)
        }
      )
    } else {
      console.log('没有找到localbalanceL1或为空')
    }

    console.log('解析完成，最终资产列表:', allAssetList.value)
  }
  const plainList = computed(() => {
    console.log('调试信息plainList:', allAssetList.value)
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
        label: 'ORDX',
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
