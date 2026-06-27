import { ref } from 'vue'
import { useToast } from '@/components/ui/toast-new'
import { storeToRefs } from 'pinia'
import { useWalletStore } from '@/store'
import { useChannelStore } from '@/store/channel'
import { useL1Store } from '@/store/l1'
import { useAssetOperations } from '@/composables/useAssetOperations'
import walletManager from '@/utils/sat20'
import satsnetStp from '@/utils/stp'
import { sleep } from 'radash'
import { useQueryClient } from '@tanstack/vue-query'

export function useAssetActions() {
  const walletStore = useWalletStore()
  const channelStore = useChannelStore()
  const l1Store = useL1Store()
  const queryClient = useQueryClient()
  const { toast } = useToast()
  const { address, feeRate, btcFeeRate } = storeToRefs(walletStore)

  // const { handleAssetOperation } = useAssetOperations()

  const loading = ref(false)

  const refreshL1Assets = () => {
    queryClient.invalidateQueries({ queryKey: ['summary-l1'] })
    queryClient.invalidateQueries({ queryKey: ['ns-l1'] })
  }

  const refreshL2Assets = () => {
    queryClient.invalidateQueries({ queryKey: ['summary-l2'] })
  }

  // 错误处理函数
  const handleError = (message: string) => {
    toast({
      title: 'Error',
      description: message,
    })
  }

  const checkChannel = async (chanid: string) => {
    const [err, result] = await satsnetStp.getChannelStatus(chanid)
    return !(err || result !== 16)
  }

  const splicingIn = async ({
    chanid,
    utxos = [],
    amt,
    feeUtxos = [],
    feeRate = btcFeeRate.value,
    asset_name,
  }: any): Promise<void> => {
    loading.value = true
    const [err] = await satsnetStp.splicingIn(chanid?.toString(), asset_name, utxos, feeUtxos, feeRate, amt)
    if (err) {
      handleError(err.message)
      loading.value = false
      return
    }
    refreshL1Assets()
    await channelStore.getAllChannels()
    toast({
      title: 'Success',
      description: 'Splicing In successful',
      duration: 1500,
    })
    loading.value = false
  }

  const splicingOut = async ({
    chanid,
    toAddress,
    amt,
    feeRate = btcFeeRate.value,
    asset_name,
  }: any): Promise<void> => {
    loading.value = true
    const feeUtxos = l1Store.plainList?.[0]?.utxos || []
    const [err] = await satsnetStp.splicingOut(chanid, toAddress, asset_name, feeUtxos, feeRate, amt)
    if (err) {
      handleError(err.message)
      loading.value = false
      return
    }
    refreshL1Assets()
    await channelStore.getAllChannels()
    toast({
      title: 'Success',
      description: 'Splicing Out successful',
      duration: 1500,
    })
    loading.value = false
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
    //console.log('UseAssetAction deposit:', toAddress, asset_name, amt, utxos, fees)
    const [err] = await walletManager.deposit(
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
        duration:1500,
      })
      loading.value = false
      return
    }
    loading.value = false
    refreshL1Assets()
    await channelStore.getAllChannels()
    toast({
      title: 'Success',
      description: 'Deposit successful',
      duration:1500,
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
    const [err] = await walletManager.withdraw(
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
        duration:1500,
      })
      loading.value = false
      return
    }

    loading.value = false
    refreshL2Assets()
    await channelStore.getAllChannels()
    toast({
      title: 'Success',
      description: 'Withdraw successful',
      duration:1500,
    })
  }

  const unlockUtxo = async ({ chanid, amt, feeUtxos = [], asset_name }: any) => {
    loading.value = true
    const status = await checkChannel(chanid)
    if (!status) {
      handleError('Channel transaction has not been confirmed')
      loading.value = false
      return
    }
    const [err] = await satsnetStp.unlockFromChannel(chanid, asset_name, amt, feeUtxos)
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
      description: 'Unlock UTXO successful',
      duration: 1500,
    })
    loading.value = false
  }

  const lockUtxo = async ({ chanid, amt, feeUtxos = [], asset_name }: any) => {
    loading.value = true
    const [err] = await satsnetStp.lockToChannel(chanid, asset_name, amt, [], feeUtxos)
    if (err) {
      handleError(err.message)
      loading.value = false
      return
    }
    await channelStore.getAllChannels()
    refreshL2Assets()
    toast({
      title: 'Success',
      description: 'Lock UTXO successful',
      duration: 1500,
    })
    loading.value = false
  }

  const lockUtxoWithExpand = async ({ chanid, amt, asset_name, feeRate = btcFeeRate.value }: any) => {
    loading.value = true
    const [err] = await satsnetStp.lockToChannelWithExpand(chanid, asset_name, amt, feeRate)
    if (err) {
      handleError(err.message)
      loading.value = false
      return false
    }
    await channelStore.getAllChannels()
    refreshL2Assets()
    toast({
      title: 'Success',
      description: 'Lock UTXO successful',
      duration: 1500,
    })
    loading.value = false
    return true
  }


  // L1 发送操作
  const l1Send = async ({ toAddress, asset_name, amt }: any) => {
    loading.value = true
    console.log('UseAssetAction l1Send:', toAddress, asset_name, amt,  btcFeeRate.value)
    const [err] = await walletManager.sendAssets(toAddress, asset_name, amt, btcFeeRate.value)
    if (err) {
      handleError(err.message)
      loading.value = false
      return
    }
    refreshL1Assets()
    toast({
      title: 'Success',
      description: 'L1 Send successful',
      duration:1500,
    })
    loading.value = false
  }

  // L2 发送操作
  const l2Send = async ({ toAddress, asset_name, amt }: any) => {
    loading.value = true
    console.log('UseAssetAction l2Send:', toAddress, asset_name, amt)
    const [err] = await walletManager.sendAssets_SatsNet(toAddress, asset_name, amt, "")
    if (err) {
      handleError(err.message)
      loading.value = false
      return
    }
    refreshL2Assets()
    toast({
      title: 'Success',
      description: 'L2 Send successful',
      duration:1500,
    })
    loading.value = false
  }

  return {
    splicingIn,
    splicingOut,
    deposit,
    withdraw,
    unlockUtxo,
    lockUtxo,
    lockUtxoWithExpand,
    l1Send,
    l2Send,
    handleError,
    loading,
  }
}
