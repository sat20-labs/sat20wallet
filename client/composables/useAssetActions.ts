import { ref } from 'vue'
import { useToast } from '@/components/ui/toast/use-toast'
import { useWalletStore } from '@/store'
import { useChannelStore } from '@/store/channel'
import { useL1Store } from '@/store/l1'
import { useL1Assets, useL2Assets } from '@/composables'
import { useAssetOperations } from '@/composables/useAssetOperations'
import satsnetStp from '@/utils/stp'
import { sleep } from 'radash'

export function useAssetActions() {
  const walletStore = useWalletStore()
  const channelStore = useChannelStore()
  const l1Store = useL1Store()
  const { refreshL1Assets } = useL1Assets()
  const { refreshL2Assets } = useL2Assets()
  const { toast } = useToast()
  const { address, feeRate, btcFeeRate } = storeToRefs(walletStore)

  // const { handleAssetOperation } = useAssetOperations()

  const loading = ref(false)

  // 错误处理函数
  const handleError = (message: string) => {
    toast({
      title: 'Error',
      description: message,
    })
  }

  // 检查通道状态
  const checkChannel = async (chanid: string) => {
    const [err, result] = await satsnetStp.getChannelStatus(chanid)
    if (err || result !== 16) {
      return false
    }
    return true
  }

  // Splicing In 操作
  const splicingIn = async ({
    chanid,
    utxos,
    amt,
    feeUtxos = [],
    feeRate,
    asset_name,
  }: any): Promise<void> => {
    loading.value = true
    console.log('UseAssetAction splicingIn:', chanid, utxos, amt, feeUtxos, feeRate, asset_name)
    const [err] = await satsnetStp.splicingIn(chanid, asset_name, utxos, feeUtxos, feeRate, amt)
    if (err) {
      loading.value = false
      handleError(err.message)
      return
    }
    refreshL1Assets()
    await channelStore.getAllChannels()
    loading.value = false
    //handleAssetOperation('splicing_in', { id: chanid, type: 'channel', protocol: 'btc' })
  }

  // Splicing Out 操作
  const splicingOut = async ({
    chanid,
    toAddress,
    amt,
    feeRate,
    asset_name,
  }: any): Promise<void> => {
    loading.value = true
    const feeUtxos = l1Store.plainList?.[0]?.utxos || []
    console.log('UseAssetAction splicingOut:', chanid, toAddress, amt, feeUtxos, feeRate, asset_name)
    const [err] = await satsnetStp.splicingOut(chanid, toAddress, asset_name, feeUtxos, feeRate, amt)
    if (err) {
      loading.value = false
      handleError(err.message)
      return
    }
    refreshL1Assets()
    await channelStore.getAllChannels()
    loading.value = false
    //handleAssetOperation('splicing_out', { id: chanid, type: 'channel', protocol: 'btc' })
  }

  // Deposit 操作
  const deposit = async ({
    toAddress,
    asset_name,
    amt,
    utxos = [],
    fees = [],
  }: any) => {
    loading.value = true
    console.log('UseAssetAction deposit:', toAddress, asset_name, amt, utxos, fees)
    const [err, result] = await satsnetStp.deposit(
      toAddress,
      asset_name,
      amt,
      utxos,
      fees,
      btcFeeRate.value
    )
    if (err) {
      toast({
        title: 'error',
        description: err.message,
      })
      loading.value = false
      return
    }
    loading.value = false
    refreshL1Assets()
    await channelStore.getAllChannels()
    toast({
      title: 'success',
      description: 'deposit success',
    })
  }
  
  // Withdraw 操作
  const withdraw = async ({
    toAddress,
    asset_name,
    amt,
    utxos = [],
    fees = [],
  }: any) => {
    loading.value = true
    console.log('UseAssetAction withdraw:', toAddress, asset_name, amt, utxos, fees)
    const [err, result] = await satsnetStp.withdraw(
      toAddress,
      asset_name,
      amt,
      utxos,
      fees,
      btcFeeRate.value
    )
    if (err) {
      toast({
        title: 'error',
        description: err.message,
      })
      loading.value = false
      return
    }
  
    loading.value = false
    refreshL2Assets()
    await channelStore.getAllChannels()
    toast({
      title: 'success',
      description: 'withdraw success',
    })
  }

  // Unlock UTXO 操作
  const unlockUtxo = async ({ chanid, amt, feeUtxos = [], asset_name }: any) => {
    loading.value = true
    const status = await checkChannel(chanid)
    if (!status) {
      handleError('Channel transaction has not been confirmed')
      loading.value = false
      return
    }
    const [err] = await satsnetStp.unlockFromChannel(chanid, asset_name, amt, [])
    if (err) {
      handleError(err.message)
      loading.value = false
      return
    }
    await sleep(1000)
    await channelStore.getAllChannels()
    refreshL2Assets()
    toast({
      title: 'Success',
      description: 'Unlock successful',
    })
    loading.value = false
    //handleAssetOperation('unlock', { id: chanid, type: 'channel', protocol: 'btc' })
  }

  // Lock UTXO 操作
  const lockUtxo = async ({ utxos, chanid, amt, feeUtxos = [], asset_name }: any) => {
    loading.value = true
    const [err] = await satsnetStp.lockToChannel(chanid, asset_name, amt, utxos, feeUtxos)
    if (err) {
      handleError(err.message)
      loading.value = false
      return
    }
    await channelStore.getAllChannels()
    refreshL2Assets()
    toast({
      title: 'Success',
      description: 'Lock successful',
    })
    loading.value = false
    //handleAssetOperation('lock', { id: chanid, type: 'channel', protocol: 'btc' })
  }

  // L1 发送操作
  const l1Send = async ({ toAddress, asset_name, amt }: any) => {
    loading.value = true
    console.log('UseAssetAction l1Send:', toAddress, asset_name, amt)
    const [err] = await satsnetStp.sendAssets(toAddress, asset_name, amt, 0)
    if (err) {
      handleError(err.message)
      loading.value = false
      return
    }
    refreshL1Assets()
    toast({
      title: 'Success',
      description: 'Send successful',
    })
    loading.value = false
    //handleAssetOperation('l1_send', { id: asset_name, type: 'l1', protocol: 'btc' })
  }

  // L2 发送操作
  const l2Send = async ({ toAddress, asset_name, amt }: any) => {
    loading.value = true
    console.log('UseAssetAction l2Send:', toAddress, asset_name, amt)
    const [err] = await satsnetStp.sendAssets_SatsNet(toAddress, asset_name, amt)
    if (err) {
      handleError(err.message)
      loading.value = false
      return
    }
    refreshL2Assets()
    toast({
      title: 'Success',
      description: 'Send successful',
    })
    loading.value = false
    //handleAssetOperation('l2_send', { id: asset_name, type: 'l2', protocol: 'btc' })
  }

  return {
    splicingIn,
    splicingOut,
    deposit,
    withdraw,
    unlockUtxo,
    lockUtxo,
    l1Send,
    l2Send,
    handleError,
    loading,
  }
}