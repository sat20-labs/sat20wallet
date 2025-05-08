import { defineStore } from 'pinia'

export const useTranscendingModeStore = defineStore('transcendingMode', {
  state: () => ({
    selectedTranscendingMode: (['poolswap', 'lightning'].includes(localStorage.getItem('selectedTranscendingMode') || '')
      ? (localStorage.getItem('selectedTranscendingMode') as 'poolswap' | 'lightning')
      : 'poolswap'),
  }),
  actions: {
    setMode(mode: 'poolswap' | 'lightning') {
      this.selectedTranscendingMode = mode
      localStorage.setItem('selectedTranscendingMode', mode) // 持久化到 localStorage
      //console.log(`Mode set to: ${mode}`) // Uncommenting the console log
    },
  },
})