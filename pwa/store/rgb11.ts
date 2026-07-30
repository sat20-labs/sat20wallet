import { defineStore } from 'pinia'
import { ref } from 'vue'

export type RGB11SyncStatus = 'idle' | 'syncing' | 'error'
export type RGB11ConsistencyStatus = 'ok' | 'warning' | 'broken'
export type RGB11BackupStatus = 'synced' | 'pending' | 'warning' | 'conflict' | 'offline' | 'not_configured'

export interface RGB11StateDTO {
  initialized: boolean
  sync_status: RGB11SyncStatus
  consistency_status: RGB11ConsistencyStatus
  backup_status: RGB11BackupStatus
  backup_enabled: boolean
  backup_mode: '' | 'autopay' | 'temporary'
  backup_retention_ms: number
  ticker_infos: any[]
  assets: any[]
  available_assets: any[]
  pending_assets: any[]
  outputs: any[]
  proofs: any[]
  transfers: any[]
}

const emptyState = (): RGB11StateDTO => ({
  initialized: false,
  sync_status: 'idle',
  consistency_status: 'warning',
  backup_status: 'offline',
  backup_enabled: false,
  backup_mode: '',
  backup_retention_ms: 0,
  ticker_infos: [],
  assets: [],
  available_assets: [],
  pending_assets: [],
  outputs: [],
  proofs: [],
  transfers: [],
})

export const useRGB11Store = defineStore('rgb11', () => {
  const state = ref<RGB11StateDTO>(emptyState())
  const error = ref('')

  const setState = (next: RGB11StateDTO) => {
    state.value = {
      ...emptyState(),
      ...next,
      ticker_infos: next.ticker_infos || [],
      assets: next.assets || [],
      available_assets: next.available_assets || [],
      pending_assets: next.pending_assets || [],
      outputs: next.outputs || [],
      proofs: next.proofs || [],
      transfers: next.transfers || [],
    }
    error.value = ''
  }

  const setError = (message: string) => {
    error.value = message
  }

  const reset = () => {
    state.value = emptyState()
    error.value = ''
  }

  return { state, error, setState, setError, reset }
})
