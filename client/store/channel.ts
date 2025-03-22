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
  remotebalanceL1: any[]
  commitheight: number
  lastpaymentid: string
  totalsent: number
  totalrecv: number
  localutxoL2: any
  localunhandledutxos_L2: any[]
  remoteutxoL2: any
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
    try {
      const [err, result] = await satsnetStp.getAllChannels()
      // console.log('获取通道结果:', { err, result })

      if (err) {
        console.error('获取通道失败:', err)
        channels.value = []
        return
      }

      if (result?.channels) {
        const channelsData = JSON.parse(result.channels)
        // console.log('解析后的通道数据:', channelsData)

        if (channelsData && typeof channelsData === 'object') {
          const values = Object.values(channelsData)
            .filter((v: any) => {
              const status = v.status
              return (status > 15 && status < 257) || (status > 0 && status < 5)
            })

          channels.value = values as Channel[]
          // console.log('过滤后的通道列表:', channels.value)

          // 立即解析资产
          await parseChannel()
        }
      } else {
        channels.value = []
      }
    } catch (error) {
      console.error('处理通道数据时出错:', error)
      channels.value = []
    }
  }

  const parseChannel = async () => {
    // console.log('开始解析通道资产...')
    allAssetList.value = []

    // 1. 确保有通道数据
    if (!channel.value) {
      // console.log('没有可用的通道')
      return
    }

    // 2. 解析本地余额
    const { localbalanceL1, remotebalanceL1, localutxoL2, remoteutxoL2 } = channel.value

    // console.log('解析本地L1余额:', localbalanceL1)
    // console.log('解析远程L1余额:', remotebalanceL1)
    // console.log('本地L2 UTXOs:', localutxoL2)
    // console.log('远程L2 UTXOs:', remoteutxoL2)

    // 处理L1余额
    if (Array.isArray(localbalanceL1)) {
      for (const item of localbalanceL1) {
        try {
          if (!item?.Name) {
            // console.log('跳过无效资产项:', item)
            continue
          }

          const protocol = item.Name.Protocol
          const key = protocol
            ? `${protocol}:${item.Name.Type}:${item.Name.Ticker}`
            : '::'
          
          let amt = item.Amount?.Value || 0
          console.log(`处理资产 ${key}, 金额: ${amt}`)

          if (protocol === 'runes') {
            const [runeErr, amtRes] = await satsnetStp.runesAmtV2ToV3(key, amt)
            if (!runeErr && amtRes) {
              amt = amtRes.runeAmtInV3
              console.log('Runes 转换后金额:', amt)
            }
          }

          const assetItem = {
            id: key,
            key,
            protocol,
            type: item.Name.Type,
            ticker: item.Name.Ticker,
            label: item.Name.Type === 'e'
              ? `${item.Name.Ticker}（raresats）`
              : item.Name.Ticker,
            utxos: [],
            amount: amt,
            bindingSat: item.BindingSat || 0
          }

          // console.log('添加资产项:', assetItem)
          allAssetList.value.push(assetItem)

          // 获取资产详情
          if (key !== '::') {
            await updateAssetInfo(key)
          }
        } catch (error) {
          console.error('处理资产项时出错:', error)
        }
      }
    }

    // 处理L2 UTXOs
    if (Array.isArray(localutxoL2)) {
      for (const utxo of localutxoL2) {
        try {
          const assets = utxo.OutValue?.Assets
          if (Array.isArray(assets)) {
            for (const asset of assets) {
              const protocol = asset.Name?.Protocol || ''
              const key = protocol
                ? `${protocol}:${asset.Name.Type}:${asset.Name.Ticker}`
                : '::'
              
              let amt = asset.Amount?.Value || 0
              console.log(`处理L2 UTXO资产 ${key}, 金额: ${amt}`)

              // 查找或创建资产项
              let assetItem = allAssetList.value.find(a => a.key === key)
              if (!assetItem) {
                assetItem = {
                  id: key,
                  key,
                  protocol,
                  type: asset.Name.Type,
                  ticker: asset.Name.Ticker,
                  label: asset.Name.Type === 'e'
                    ? `${asset.Name.Ticker}（raresats）`
                    : asset.Name.Ticker,
                  utxos: [],
                  amount: 0,
                  bindingSat: 0
                }
                allAssetList.value.push(assetItem)
              }

              // 更新UTXO信息
              assetItem.utxos.push({
                id: utxo.UtxoId,
                outpoint: utxo.OutPointStr,
                value: amt
              })
              assetItem.amount += amt

              if (key !== '::') {
                await updateAssetInfo(key)
              }
            }
          }
        } catch (error) {
          console.error('处理L2 UTXO时出错:', error)
        }
      }
    }

    console.log('资产解析完成，列表:', allAssetList.value)
  }

  // 提取资产信息更新逻辑
  const updateAssetInfo = async (key: string) => {
    try {
      const [infoErr, infoRes] = await satsnetStp.getTickerInfo(key)
      if (!infoErr && infoRes?.ticker) {
        const tickerInfo = JSON.parse(infoRes.ticker)
        if (tickerInfo?.displayname) {
          const asset = allAssetList.value.find(a => a.key === key)
          if (asset) {
            asset.label = tickerInfo.displayname
            console.log(`更新资产 ${key} 标签为: ${asset.label}`)
          }
        }
      }
    } catch (e) {
      console.error(`获取资产 ${key} 信息失败:`, e)
    }
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
