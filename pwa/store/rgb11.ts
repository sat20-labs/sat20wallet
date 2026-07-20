import { defineStore } from 'pinia'
import { ref } from 'vue'

export type RGB11SyncStatus = 'idle' | 'syncing' | 'error'
export type RGB11ConsistencyStatus = 'ok' | 'warning' | 'broken'
export type RGB11DKVSStatus = 'synced' | 'pending' | 'warning' | 'conflict' | 'offline' | 'not_configured'

export interface RGB11StateDTO {
  initialized: boolean
  sync_status: RGB11SyncStatus
  consistency_status: RGB11ConsistencyStatus
  dkvs_status: RGB11DKVSStatus
  auto_backup_enabled: boolean
  ticker_infos: any[]
  assets: any[]
  outputs: any[]
  proofs: any[]
  transfers: any[]
}

const emptyState = (): RGB11StateDTO => ({
  initialized: false,
  sync_status: 'idle',
  consistency_status: 'warning',
  dkvs_status: 'offline',
  auto_backup_enabled: false,
  ticker_infos: [],
  assets: [],
  outputs: [],
  proofs: [],
  transfers: [],
})

export const useRGB11Store = defineStore('rgb11', () => {
  const state = ref<RGB11StateDTO>(emptyState())

  const setState = (next: RGB11StateDTO) => {
    state.value = {
      ...emptyState(),
      ...next,
      ticker_infos: next.ticker_infos || [],
      assets: next.assets || [],
      outputs: next.outputs || [],
      proofs: next.proofs || [],
      transfers: next.transfers || [],
    }
  }

  const reset = () => {
    state.value = emptyState()
  }

  return { state, setState, reset }
})
