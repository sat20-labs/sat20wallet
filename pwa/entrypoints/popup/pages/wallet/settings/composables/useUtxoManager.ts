import { ref, computed, watch, onScopeDispose, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useWalletStore } from '@/store/wallet'
import { useGlobalStore } from '@/store/global'
import { storeToRefs } from 'pinia'
import walletManager from '@/utils/sat20'
import ordxApi from '@/apis/ordx'
import { useToast } from '@/components/ui/toast-new'
import { LockedUtxoInfo, FailedUtxoInfo, LockedUtxosApiResponse, LockedUtxoApiResponse } from '@/types'

export type UtxoTab = 'btc' | 'satsnet' | 'ordinals' | 'rgb'

export function useUtxoManager(tab: Ref<UtxoTab>) {
  const { t } = useI18n()
  const walletStore = useWalletStore()
  const globalStore = useGlobalStore()
  const { toast } = useToast()

  const { address, network } = storeToRefs(walletStore)
  const { env } = storeToRefs(globalStore)

  // State
  const lockedUtxos = ref<LockedUtxoInfo[]>([])
  const loading = ref(false)
  const lockLoading = ref(false)
  const unlockingIdx = ref(-1)
  const unlockOrdinalsLoading = ref(false)
  const selectedOrdinals = ref<string[]>([])

  // Computed
  const addressStr = computed(() => address.value || '')
  const loadError = ref('')
  const lockNetwork = ref('')
  const identityKey = computed(() => JSON.stringify([
    walletStore.rootAccountId, walletStore.walletId, walletStore.accountIndex,
    network.value, walletStore.wallet?.fingerprint, walletStore.locked,
    walletStore.isSwitchingWallet, walletStore.isSwitchingAccount, walletStore.isSwitchingNetwork,
  ]))
  let generation = 0

  // Methods
  const fetchLockedUtxos = async (requested: UtxoTab) => {
    if (requested !== tab.value) return
    const request = ++generation
    const identity = identityKey.value
    const current = () => request === generation && identity === identityKey.value && requested === tab.value
    lockedUtxos.value = []
    selectedOrdinals.value = []
    loadError.value = ''
    lockNetwork.value = network.value
    loading.value = false
    if (walletStore.locked || walletStore.isSwitchingWallet || walletStore.isSwitchingAccount || walletStore.isSwitchingNetwork) return
    loading.value = true
    try {
      let rows: LockedUtxoInfo[]
      if (requested === 'ordinals') {
        const response = await ordxApi.getLockedUtxos({ address: addressStr.value, network: network.value }) as LockedUtxosApiResponse
        rows = (response?.data || []).map((utxo: LockedUtxoApiResponse) => {
          const [txid, vout] = utxo.Outpoint.split(':')
          const asset = utxo.Assets?.[0]
          return { utxo: utxo.Outpoint, txid, vout, reason: asset
            ? `${asset.Name.Protocol}:${asset.Name.Type}:${asset.Name.Ticker || ''}`
            : t('settingsUi.unknownAsset') }
        })
      } else if (requested === 'rgb') {
        const [error, view] = await walletManager.getRGB11View()
        if (error) throw error
        if (!view || !Array.isArray(view.locks) || !view.lock_scope) throw new Error(t('utxoManager.updateRuntime'))
        rows = view.locks.map(lock => ({
          utxo: lock.outpoint, txid: lock.outpoint.split(':')[0], vout: lock.outpoint.split(':')[1],
          category: 'rgb', chain: lock.chain, scope: lock.scope, reason: lock.reason,
          lockedTime: lock.locked_time, value: lock.value, assets: lock.assets, transferIDs: lock.transfer_ids,
        }))
        if (current()) lockNetwork.value = view.lock_scope.network
      } else {
        const [error, result] = requested === 'btc'
          ? await walletManager.getAllLockedUtxo(addressStr.value)
          : await walletManager.getAllLockedUtxo_SatsNet(addressStr.value)
        if (error) throw error
        if (!result) throw new Error(t('utxoManager.loadFailed'))
        rows = Object.entries(result).map(([outpoint, raw]) => {
          const info = JSON.parse(raw as string)
          // Never expose an unclassified protection lock as an Unlock row.
          // The current SDK supplies this field independently of RGB queries.
          if (!['manual', 'rgb', 'other'].includes(info?.category)) throw new Error(t('utxoManager.updateRuntime'))
          return { ...info, utxo: outpoint, txid: outpoint.split(':')[0], vout: outpoint.split(':')[1] }
        }).filter(row => row.category !== 'rgb')
      }
      if (current()) lockedUtxos.value = rows
    } catch (error) {
      if (current()) loadError.value = error instanceof Error ? error.message : t('utxoManager.loadFailed')
    } finally {
      if (current()) loading.value = false
    }
  }

  watch([tab, identityKey], () => { void fetchLockedUtxos(tab.value) }, { immediate: true, flush: 'sync' })
  onScopeDispose(() => { generation++ })

  const lockUtxo = async (utxoInput: string, tab: 'btc' | 'satsnet') => {
    if (!utxoInput) {
      toast({ title: t('approvalUi.error'), description: t('settingsUi.enterUtxo'), variant: 'destructive' })
      return
    }

    lockLoading.value = true
    let err

    if (tab === 'btc') {
      [err] = await walletManager.lockUtxo(addressStr.value, utxoInput, 'manual')
    } else {
      [err] = await walletManager.lockUtxo_SatsNet(addressStr.value, utxoInput, 'manual')
    }

    lockLoading.value = false

    if (err) {
      toast({ title: t('approvalUi.error'), description: t('settingsUi.lockFailed'), variant: 'destructive' })
    } else {
      toast({ title: t('approvalUi.success'), description: t('settingsUi.lockSuccess'), variant: 'success' })
      await fetchLockedUtxos(tab)
    }
  }

  const unlockUtxo = async (idx: number, utxo: LockedUtxoInfo, tab: 'btc' | 'satsnet') => {
    if (utxo.category === 'rgb') return
    unlockingIdx.value = idx
    let err

    if (tab === 'btc') {
      [err] = await walletManager.unlockUtxo(addressStr.value, utxo.utxo)
    } else {
      [err] = await walletManager.unlockUtxo_SatsNet(addressStr.value, utxo.utxo)
    }

    unlockingIdx.value = -1

    if (err) {
      toast({ title: t('approvalUi.error'), description: t('settingsUi.unlockFailed'), variant: 'destructive' })
    } else {
      toast({ title: t('approvalUi.success'), description: t('settingsUi.unlockSuccess'), variant: 'success' })
      await fetchLockedUtxos(tab)
    }
  }

  // Ordinals UTXO management functions
  const toggleSelectUtxo = (utxo: string) => {
    console.log('toggleSelectUtxo called with:', utxo)
    console.log('current selectedOrdinals:', selectedOrdinals.value)
    const index = selectedOrdinals.value.indexOf(utxo)
    if (index > -1) {
      selectedOrdinals.value.splice(index, 1)
      console.log('removed utxo, new selectedOrdinals:', selectedOrdinals.value)
    } else {
      selectedOrdinals.value.push(utxo)
      console.log('added utxo, new selectedOrdinals:', selectedOrdinals.value)
    }
  }

  const toggleSelectAll = () => {
    if (selectedOrdinals.value.length === lockedUtxos.value.length) {
      selectedOrdinals.value = []
    } else {
      selectedOrdinals.value = lockedUtxos.value.map(utxo => utxo.utxo)
    }
  }

  const unlockSelectedOrdinals = async () => {
    if (selectedOrdinals.value.length === 0) {
      toast({ title: t('approvalUi.error'), description: t('settingsUi.selectUtxo'), variant: 'destructive' })
      return
    }

    unlockOrdinalsLoading.value = true

    try {
      // Get public key
      const [pubKeyErr, pubKeyRes] = await walletManager.getWalletPubkey(walletStore.accountIndex)
      if (pubKeyErr || !pubKeyRes) {
        throw new Error(t('settingsUi.getPublicKeyFailed'))
      }

      // Sign the UTXOs data
      const utxosJson = JSON.stringify(selectedOrdinals.value)
      const [sigErr, sigRes] = await walletManager.signData(utxosJson)
      if (sigErr || !sigRes) {
        throw new Error(t('settingsUi.signDataFailed'))
      }

      // Call unlock API with hex strings directly
      const response = await ordxApi.unlockOrdinals({
        utxos: selectedOrdinals.value,
        pubKey: pubKeyRes.pubKey, // Use hex string directly
        sig: sigRes.signature, // Use hex string directly
        network: network.value
      })

      if (response.failedUtxos && response.failedUtxos.length > 0) {
        const failedMessages = response.failedUtxos.map((failed: FailedUtxoInfo) =>
          `${failed.utxo}: ${failed.reason}`
        ).join('\n')
        toast({
          title: t('settingsUi.partialSuccess'),
          description: t('settingsUi.unlockPartial', { errors: failedMessages }),
          variant: 'destructive'
        })
      } else {
        toast({
          title: t('approvalUi.success'),
          description: t('settingsUi.unlockCount', { count: selectedOrdinals.value.length }),
          variant: 'success'
        })
      }

      // Clear selection and refresh data
      selectedOrdinals.value = []
      await fetchLockedUtxos('ordinals')

    } catch (error: any) {
      console.error('Failed to unlock ordinals UTXOs:', error)
      toast({
        title: t('approvalUi.error'),
        description: t('settingsUi.unlockError', { error: error.message }),
        variant: 'destructive'
      })
    } finally {
      unlockOrdinalsLoading.value = false
    }
  }

  return {
    // State
    lockedUtxos,
    loadError,
    lockNetwork,
    loading,
    lockLoading,
    unlockingIdx,
    unlockOrdinalsLoading,
    selectedOrdinals,
    addressStr,

    // Methods
    fetchLockedUtxos,
    lockUtxo,
    unlockUtxo,
    toggleSelectUtxo,
    toggleSelectAll,
    unlockSelectedOrdinals
  }
}
