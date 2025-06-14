import { defineStore } from 'pinia'
import { ref } from 'vue'

/**
 * 可用的交易模式类型
 */
export type TranscendingMode = 'poolswap' | 'lightning'

/**
 * 存储键名
 */
const STORAGE_KEY = 'selectedTranscendingMode'

/**
 * 默认模式
 */
const DEFAULT_MODE: TranscendingMode = 'poolswap'

/**
 * 有效的模式列表
 */
const VALID_MODES: TranscendingMode[] = ['poolswap', 'lightning']

/**
 * 交易模式 Store
 * 用于管理应用中的交易模式状态
 */
export const useTranscendingModeStore = defineStore('transcendingMode', () => {
  // 从 localStorage 获取初始值，如果无效则使用默认值
  const getInitialMode = (): TranscendingMode => {
    const storedMode = localStorage.getItem(STORAGE_KEY)
    return VALID_MODES.includes(storedMode as TranscendingMode) 
      ? (storedMode as TranscendingMode)
      : DEFAULT_MODE
  }

  // 当前选择的交易模式
  const selectedTranscendingMode = ref<TranscendingMode>(getInitialMode())

  /**
   * 设置新的交易模式
   * @param mode - 要设置的新模式
   * @throws {Error} 当提供的模式无效时抛出错误
   */
  const setMode = (mode: TranscendingMode) => {
    if (!VALID_MODES.includes(mode)) {
      throw new Error(`Invalid mode: ${mode}. Valid modes are: ${VALID_MODES.join(', ')}`)
    }
    
    try {
      selectedTranscendingMode.value = mode
      localStorage.setItem(STORAGE_KEY, mode)
    } catch (error) {
      console.error('Failed to set mode:', error)
      throw new Error('Failed to persist mode selection')
    }
  }

  /**
   * 重置为默认模式
   */
  const resetToDefault = () => {
    setMode(DEFAULT_MODE)
  }

  return {
    selectedTranscendingMode,
    setMode,
    resetToDefault,
  }
})