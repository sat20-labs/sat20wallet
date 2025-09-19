import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useWalletStore } from '@/store/wallet'
import { useGlobalStore } from '@/store/global'
import { storeToRefs } from 'pinia'
import satsnetStp from '@/utils/stp'
import walletManager from '@/utils/sat20'
import ordxApi from '@/apis/ordx'
import { useToast } from '@/components/ui/toast-new'
import { LockedUtxoInfo, FailedUtxoInfo, LockedUtxosApiResponse, LockedUtxoApiResponse } from '@/types'

export function useUtxoManager() {
  const { t } = useI18n()
  const walletStore = useWalletStore()
  const globalStore = useGlobalStore()
  const { toast } = useToast()
  
  const { address, network } = walletStore
  const { env } = storeToRefs(globalStore)
  
  // State
  const lockedUtxos = ref<LockedUtxoInfo[]>([])
  const loading = ref(false)
  const lockLoading = ref(false)
  const unlockingIdx = ref(-1)
  const unlockOrdinalsLoading = ref(false)
  const selectedOrdinals = ref<string[]>([])
  
  // Computed
  const addressStr = computed(() => address || '')
  
  // Methods
  const fetchLockedUtxos = async (tab: 'btc' | 'satsnet' | 'ordinals') => {
    loading.value = true
    let res
    
    if (tab === 'ordinals') {
      // Fetch locked ordinals UTXOs from API
      try {
        const apiResponse = await ordxApi.getLockedUtxos({ 
          address: addressStr.value, 
          network: network === 'livenet' ? 'mainnet' : 'testnet' 
        }) as LockedUtxosApiResponse
        
        lockedUtxos.value = (apiResponse?.data || []).map((utxo: LockedUtxoApiResponse) => {
          const [txid, vout] = utxo.Outpoint.split(':')
          const assetInfo = utxo.Assets?.[0]
          const reason = assetInfo 
            ? `${assetInfo.Name.Protocol}:${assetInfo.Name.Type}:${assetInfo.Name.Ticker || ''}` 
            : 'Unknown asset'
          
          return {
            utxo: utxo.Outpoint,
            txid,
            vout,
            reason,
            lockedTime: undefined // API doesn't provide locked time
          }
        })
      } catch (error) {
        console.error('Failed to fetch locked ordinals UTXOs:', error)
        lockedUtxos.value = []
      }
    } else {
      // Fetch from local storage (BTC/SatoshiNet)
      if (tab === 'btc') {
        [, res] = await satsnetStp.getAllLockedUtxo(addressStr.value)
      } else {
        [, res] = await satsnetStp.getAllLockedUtxo_SatsNet(addressStr.value)
      }
      lockedUtxos.value = Object.entries(res || {}).map(([utxo, infoStr]) => {
        let info
        try {
          info = JSON.parse(infoStr)
        } catch (e) {
          info = {}
        }
        return {
          utxo,
          txid: utxo.split(':')[0],
          vout: utxo.split(':')[1],
          ...info
        }
      })
    }
    
    // Clear selected ordinals when switching tabs
    if (tab !== 'ordinals') {
      selectedOrdinals.value = []
    }
    
    loading.value = false
  }
  
  const lockUtxo = async (utxoInput: string, tab: 'btc' | 'satsnet') => {
    if (!utxoInput) {
      toast({ title: 'Error', description: '请输入UTXO', variant: 'destructive' })
      return
    }
    
    lockLoading.value = true
    let err
    
    if (tab === 'btc') {
      [err] = await satsnetStp.lockUtxo(addressStr.value, utxoInput)
    } else {
      [err] = await satsnetStp.lockUtxo_SatsNet(addressStr.value, utxoInput)
    }
    
    lockLoading.value = false
    
    if (err) {
      toast({ title: 'Error', description: '锁定失败', variant: 'destructive' })
    } else {
      toast({ title: 'Success', description: '锁定成功', variant: 'success' })
      await fetchLockedUtxos(tab)
    }
  }
  
  const unlockUtxo = async (idx: number, utxo: LockedUtxoInfo, tab: 'btc' | 'satsnet') => {
    unlockingIdx.value = idx
    let err
    
    if (tab === 'btc') {
      [err] = await satsnetStp.unlockUtxo(addressStr.value, utxo.utxo)
    } else {
      [err] = await satsnetStp.unlockUtxo_SatsNet(addressStr.value, utxo.utxo)
    }
    
    unlockingIdx.value = -1
    
    if (err) {
      toast({ title: 'Error', description: '解锁失败', variant: 'destructive' })
    } else {
      toast({ title: 'Success', description: '解锁成功', variant: 'success' })
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
      toast({ title: 'Error', description: '请选择要解锁的UTXO', variant: 'destructive' })
      return
    }

    unlockOrdinalsLoading.value = true
    
    try {
      // Get public key
      const [pubKeyErr, pubKeyRes] = await walletManager.getWalletPubkey(walletStore.accountIndex)
      if (pubKeyErr || !pubKeyRes) {
        throw new Error('Failed to get public key')
      }

      // Sign the UTXOs data
      const utxosJson = JSON.stringify(selectedOrdinals.value)
      const [sigErr, sigRes] = await walletManager.signData(utxosJson)
      if (sigErr || !sigRes) {
        throw new Error('Failed to sign data')
      }

      // Call unlock API with hex strings directly
      const response = await ordxApi.unlockOrdinals({
        utxos: selectedOrdinals.value,
        pubKey: pubKeyRes.pubKey, // Use hex string directly
        sig: sigRes.signature, // Use hex string directly
        network: network === 'livenet' ? 'mainnet' : 'testnet'
      })

      if (response.failedUtxos && response.failedUtxos.length > 0) {
        const failedMessages = response.failedUtxos.map((failed: FailedUtxoInfo) => 
          `${failed.utxo}: ${failed.reason}`
        ).join('\n')
        toast({ 
          title: 'Partial Success', 
          description: `部分UTXO解锁失败:\n${failedMessages}`, 
          variant: 'destructive' 
        })
      } else {
        toast({ 
          title: 'Success', 
          description: `成功解锁 ${selectedOrdinals.value.length} 个UTXO`, 
          variant: 'success' 
        })
      }

      // Clear selection and refresh data
      selectedOrdinals.value = []
      await fetchLockedUtxos('ordinals')

    } catch (error: any) {
      console.error('Failed to unlock ordinals UTXOs:', error)
      toast({ 
        title: 'Error', 
        description: `解锁失败: ${error.message}`, 
        variant: 'destructive' 
      })
    } finally {
      unlockOrdinalsLoading.value = false
    }
  }
  
  return {
    // State
    lockedUtxos,
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
