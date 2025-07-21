import satsnetStp from '@/utils/stp'
import { useWalletStore } from '@/store'
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

  const channel = ref<Channel | null>(null)
  const allAssetList = ref<any[]>([])
  const plainBalance = ref(0)
  const totalSats = ref(0)

  const getAllChannels = async () => {
    const [_, resull] = await satsnetStp.getAllChannels()
    const [, currentChannel] = await satsnetStp.getCurrentChannel()
    console.log('currentChannel', currentChannel)

    if (currentChannel?.json) {
      try {
        const c = JSON.parse(currentChannel.json)
        if (c.localbalanceL1) {
          channel.value = c
        } else {
          console.log('数据不完整，localbalanceL1 不存在:', c)
          channel.value = null
        }
      } catch (error) {
        console.log('解析 JSON 出错:', error)
      }
    }
  }

  const parseChannel = async () => {
    console.log('开始解析通道资产...')
    console.log('当前channel:', channel.value)

    if (!channel.value) {
      //console.log('channel.value 尚未加载，退出解析')
      return
    }

    allAssetList.value = []
    const { localbalanceL1 } = channel.value || {}
    console.log('channel. localbalanceL1:', localbalanceL1)
    //console.log('channel. localbalanceL1?.length:', localbalanceL1?.length)

    if (localbalanceL1?.length) {
      //console.log('localbalanceL1 内容:', localbalanceL1)
      for (let i = 0; i < localbalanceL1.length; i++) {
        const item = localbalanceL1[i]
        if (item.Name.Type === '') {
          totalSats.value = item.Amount
        }
        const protocol = item.Name.Protocol
        const key = protocol
          ? `${protocol}:${item.Name.Type}:${item.Name.Ticker}`
          : '::'

        const amt = Number(item.Amount) || 0
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
        //console.log('添加资产项:', assetItem)
        //console.log('allAssetList', allAssetList.value)
        //console.log('l1', localbalanceL1)
        allAssetList.value.push(assetItem)
      }
      const getAssetInfo = async (key: string) => {
        //console.log('获取资产信息:', key)
        const [err, res] = await satsnetStp.getTickerInfo(key)
        if (res?.ticker) {
          const { ticker } = res
          const result = JSON.parse(ticker)
          const findItem = allAssetList.value?.find((a: any) => a.key === key)
          if (findItem) {
            findItem.label = result?.name.Ticker || findItem.label
            console.log('更新资产标签:', findItem)
          }
        }
      }

      const tickers = allAssetList.value.map((a) => a.key)
      //console.log('待处理的ticker列表:', tickers)
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
    //console.log('调试信息plainList:', allAssetList.value)
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

  // watch(
  //   channel,
  //   () => {
  //     parseChannel()
  //   },
  //   {
  //     immediate: true,
  //     deep: true,
  //   }
  // )

  watch(
    channel,
    async (newValue) => {
      if (newValue) {
        console.log('channel 更新，等待数据稳定...')
        await nextTick() // 等待 DOM 和响应式数据更新
        parseChannel()
      }
    },
    { immediate: true, deep: true }
  )

  return {
    uniqueAssetList,
    sat20List,
    runesList,
    brc20List,
    ordList,
    plainList,
    plainBalance,
    channel,
    totalSats,
    getAllChannels,
  }
})
