import { defineStore } from 'pinia'

export const useTranscendingModeStore = defineStore('transcendingMode', {
  state: () => ({
    selectedTranscendingMode: 'poolswap' as 'poolswap' | 'lightning',
  }),
  actions: {
    setMode(mode: 'poolswap' | 'lightning') {
      this.selectedTranscendingMode = mode
    },
  },
})