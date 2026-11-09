import { ref } from 'vue'
import { useToast } from '@/components/ui/toast-new'
import { storeToRefs } from 'pinia'
import { useWalletStore } from '@/store'
import { useChannelStore } from '@/store/channel'
import { useL1Store } from '@/store/l1'
import { useAssetOperations } from '@/composables/useAssetOperations'
import walletManager from '@/utils/sat20'
import satsnetStp, { parseLockExpandRequiredAmount } from '@/utils/stp'
import { sleep } from 'radash'
import { useQueryClient } from '@tanstack/vue-query'
import { useI18n } from 'vue-i18n'

export function useAssetActions() {
  const walletStore = useWalletStore()
  const channelStore = useChannelStore()
  const l1Store = useL1Store()
  const queryClient = useQueryClient()
  const { toast } = useToast()
  const { t } = useI18n()
  const { address, feeRate, btcFeeRate } = storeToRefs(walletStore)

  // const { handleAssetOperation } = useAssetOperations()

  const loading = ref(false)

  const refreshL1Assets = () => {
    queryClient.invalidateQueries({ queryKey: ['summary-l1'] })
    queryClient.invalidateQueries({ queryKey: ['ns-l1'] })
    queryClient.invalidateQueries({ queryKey: ['rgb11-state'] })
  }

  const refreshL2Assets = () => {
    queryClient.invalidateQueries({ queryKey: ['summary-l2'] })
  }

  // 错误处理函数
  const handleError = (message: string) => {
    toast({
      title: t('common.error'),
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
    let refreshFailed = false
    try { await channelStore.getCurrentChannel() } catch (error) {
      if (!asset_name.startsWith('rgb11:')) throw error
      // A successful submission must not become a retryable transaction error.
      refreshFailed = true
    }
    toast({
      title: t('common.success'),
      description: t(refreshFailed ? 'rgb11Transfer.crossLayerSubmittedRefreshFailed'
        : asset_name.startsWith('rgb11:') ? 'rgb11Transfer.crossLayerSubmitted' : 'pwaUi.assetActions.spliceIn'),
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
    let refreshFailed = false
    try { await channelStore.getCurrentChannel() } catch (error) {
      if (!asset_name.startsWith('rgb11:')) throw error
      // A successful submission must not become a retryable transaction error.
      refreshFailed = true
    }
    toast({
      title: t('common.success'),
      description: t(refreshFailed ? 'rgb11Transfer.crossLayerSubmittedRefreshFailed'
        : asset_name.startsWith('rgb11:') ? 'rgb11Transfer.crossLayerSubmitted' : 'pwaUi.assetActions.spliceOut'),
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
        title: t('common.error'),
        description: err.message,
        duration:1500,
      })
      loading.value = false
      return
    }
    loading.value = false
    refreshL1Assets()
    await channelStore.getCurrentChannel()
    toast({
      title: t('common.success'),
      description: t('pwaUi.assetActions.deposit'),
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
        title: t('common.error'),
        description: err.message,
        duration:1500,
      })
      loading.value = false
      return
    }

    loading.value = false
    refreshL2Assets()
    await channelStore.getCurrentChannel()
    toast({
      title: t('common.success'),
      description: t('pwaUi.assetActions.withdraw'),
      duration:1500,
    })
  }

  const unlockUtxo = async ({ chanid, amt, feeUtxos = [], asset_name }: any) => {
    loading.value = true
    const status = await checkChannel(chanid)
    if (!status) {
      handleError(t('pwaUi.assetActions.channelUnconfirmed'))
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
    await channelStore.getCurrentChannel()
    refreshL2Assets()
    toast({
      title: t('common.success'),
      description: t('pwaUi.assetActions.unlock'),
      duration: 1500,
    })
    loading.value = false
  }

  const lockUtxo = async ({ chanid, amt, feeUtxos = [], asset_name }: any) => {
    loading.value = true
    const [err] = await satsnetStp.lockToChannel(chanid, asset_name, amt, [], feeUtxos)
    if (err) {
      const expandRequiredAmount = parseLockExpandRequiredAmount(err.message)
      if (expandRequiredAmount === undefined) {
        handleError(err.message)
      }
      loading.value = false
      return { ok: false as const, error: err.message, expandRequiredAmount }
    }
    await channelStore.getCurrentChannel()
    refreshL2Assets()
    toast({
      title: t('common.success'),
      description: t('pwaUi.assetActions.lock'),
      duration: 1500,
    })
    loading.value = false
    return { ok: true as const }
  }

  const lockUtxoWithExpand = async ({ chanid, amt, asset_name, feeRate = btcFeeRate.value }: any) => {
    loading.value = true
    const [err, result] = await satsnetStp.lockToChannelWithExpand(chanid, asset_name, amt, feeRate)
    if (err) {
      handleError(err.message)
      loading.value = false
      return { ok: false as const, error: err.message }
    }

    const reservationId = result?.id ?? null
    const lockTxId = result?.lockTxId ?? null
    try {
      await channelStore.getCurrentChannel()
      await refreshL2Assets()
    } catch (refreshError) {
      // The WASM call already submitted the operation. A refresh failure must
      // not turn it into a retryable error and risk a duplicate reservation.
      console.warn('Channel refresh failed after lock-with-expand submission:', refreshError)
    }
    toast({
      title: t('tools.messages.txSubmitted'),
      description: reservationId !== null
        ? t('assetOperationDialog.lockExpandSubmittedWithId', { id: reservationId })
        : t('assetOperationDialog.lockExpandSubmitted'),
      duration: 3000,
    })
    loading.value = false
    return {
      ok: true as const,
      status: 'pending' as const,
      reservationId,
      lockTxId,
    }
  }


  // L1 发送操作
  const l1Send = async ({ toAddress, asset_name, amt }: any) => {
    loading.value = true
    console.log('UseAssetAction l1Send:', toAddress, asset_name, amt,  btcFeeRate.value)
    let err: Error | undefined
    if (/^(sp1|tsp1)/i.test(String(toAddress).trim())) {
      if (asset_name !== '::') {
        err = new Error(t('pwaUi.assetActions.spBtcOnly'))
      } else {
        [err] = await walletManager.sendSilentPayment(toAddress, amt, btcFeeRate.value)
      }
    } else {
      [err] = await walletManager.sendAssets(toAddress, asset_name, amt, btcFeeRate.value)
    }
    if (err) {
      handleError(err.message)
      loading.value = false
      return
    }
    refreshL1Assets()
    toast({
      title: t('common.success'),
      description: t('pwaUi.assetActions.l1Send'),
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
      title: t('common.success'),
      description: t('pwaUi.assetActions.l2Send'),
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
