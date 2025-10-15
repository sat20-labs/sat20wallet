import { ref } from 'vue'
import { useToast } from '@/components/ui/toast-new'
import { storeToRefs } from 'pinia'
import { useWalletStore } from '@/store'
// import { useChannelStore } from '@/store/channel' - Removed channel store
import { useL1Store } from '@/store/l1'
import { useL1Assets, useL2Assets } from '@/composables'
import { useAssetOperations } from '@/composables/useAssetOperations'
import walletManager from '@/utils/sat20'

export function useAssetActions() {
  const walletStore = useWalletStore()
  // const channelStore = useChannelStore() - Removed channel store
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
    // await channelStore.getAllChannels() - Removed channel store
    toast({
      title: 'Success',
      description: 'Withdraw successful',
      duration:1500,
    })
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
    deposit,
    withdraw,
    l1Send,
    l2Send,
    handleError,
    loading,
  }
}