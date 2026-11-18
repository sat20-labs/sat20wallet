import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { RGBView } from '@/utils/walletViews'
const emptyState = (): RGBView => ({ lock_scope: { network: '', wallet_id: 0, account_index: 0 }, locks: [], assets: [], transfers: [], receives: [], syncing: false, can_write: false, error_code: '' })
export const useRGB11Store = defineStore('rgb11', () => {
  const state = ref<RGBView>(emptyState())
  const error = ref('')
  const setState = (next: RGBView) => { state.value = next; error.value = '' }
  const setError = (message: string) => { error.value = message }
  const reset = () => { state.value = emptyState(); error.value = '' }
  return { state, error, setState, setError, reset }
})
