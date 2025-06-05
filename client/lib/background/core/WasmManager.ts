import { Runtime } from 'wxt/browser'
import { browser } from 'wxt/browser'
import { walletStorage } from '@/lib/walletStorage'
import { getConfig, logLevel } from '@/config/wasm'

/**
 * WASM管理器 - 负责WASM模块的加载和初始化
 */
export class WasmManager {
  private isWasmReady = false
  private messageQueue: { port: Runtime.Port; event: any }[] = []
  private initializationPromise: Promise<void> | null = null

  /**
   * 初始化WASM模块
   */
  async initialize(): Promise<void> {
    if (this.initializationPromise) {
      return this.initializationPromise
    }

    this.initializationPromise = this.loadWasmModules()
    return this.initializationPromise
  }

  /**
   * 加载WASM模块
   */
  private async loadWasmModules(): Promise<void> {
    try {
      console.log('Starting WASM initialization...')
      
      // 加载钱包WASM
      await this.loadWalletWasm()
      
      // 加载STP WASM
      await this.loadStpWasm()
      
      // 设置就绪状态
      this.isWasmReady = true
      console.log('WASM modules loaded successfully')
      
    } catch (error) {
      console.error('Failed to load WASM modules:', error)
      this.isWasmReady = false
      throw error
    }
  }

  /**
   * 加载钱包WASM模块
   */
  private async loadWalletWasm(): Promise<void> {
    try {
      // 导入WASM执行脚本
      importScripts('/wasm/wasm_exec.js')
      
      const go = new Go()
      const env = walletStorage.getValue('env') || 'test'
      const wasmPath = browser.runtime.getURL('/wasm/sat20wallet.wasm')
      
      const response = await fetch(wasmPath)
      const wasmBinary = await response.arrayBuffer()
      const wasmModule = await WebAssembly.instantiate(wasmBinary, go.importObject)
      
      go.run(wasmModule.instance)
      
      await (globalThis as any).sat20wallet_wasm.init(getConfig(env), logLevel)
      console.log('Wallet WASM loaded successfully')
      
    } catch (error) {
      console.error('Failed to load wallet WASM:', error)
      throw error
    }
  }

  /**
   * 加载STP WASM模块
   */
  private async loadStpWasm(): Promise<void> {
    try {
      const go = new Go()
      const wasmPath = browser.runtime.getURL('/wasm/stpd.wasm')
      const env = walletStorage.getValue('env') || 'test'
      
      const response = await fetch(wasmPath)
      const wasmBinary = await response.arrayBuffer()
      const wasmModule = await WebAssembly.instantiate(wasmBinary, go.importObject)
      
      go.run(wasmModule.instance)
      
      await (globalThis as any).stp_wasm.init(getConfig(env), logLevel)
      console.log('STP WASM loaded successfully')
      
    } catch (error) {
      console.error('Failed to load STP WASM:', error)
      throw error
    }
  }

  /**
   * 重新初始化WASM模块（环境变更时使用）
   */
  async reinitialize(env: string): Promise<void> {
    try {
      console.log(`Reinitializing WASM with environment: ${env}`)
      
      // 释放现有WASM实例
      await this.releaseWasmModules()
      
      // 重新初始化
      await (globalThis as any).stp_wasm.init(getConfig(env), logLevel)
      await (globalThis as any).sat20wallet_wasm.init(getConfig(env), logLevel)
      
      console.log('WASM modules reinitialized successfully')
      
    } catch (error) {
      console.error('Failed to reinitialize WASM:', error)
      throw error
    }
  }

  /**
   * 释放WASM模块
   */
  private async releaseWasmModules(): Promise<void> {
    try {
      if ((globalThis as any).stp_wasm?.release) {
        await (globalThis as any).stp_wasm.release()
      }
      if ((globalThis as any).sat20wallet_wasm?.release) {
        await (globalThis as any).sat20wallet_wasm.release()
      }
    } catch (error) {
      console.warn('Error releasing WASM modules:', error)
    }
  }

  /**
   * 检查WASM是否就绪
   */
  isReady(): boolean {
    return this.isWasmReady
  }

  /**
   * 将消息加入队列
   */
  queueMessage(port: Runtime.Port, event: any): void {
    this.messageQueue.push({ port, event })
    console.log(`Message queued, total queued: ${this.messageQueue.length}`)
  }

  /**
   * 获取队列中的消息
   */
  getQueuedMessages(): { port: Runtime.Port; event: any }[] {
    return [...this.messageQueue]
  }

  /**
   * 清空消息队列
   */
  clearQueue(): void {
    this.messageQueue = []
  }

  /**
   * 获取队列长度
   */
  getQueueLength(): number {
    return this.messageQueue.length
  }

  /**
   * 获取初始化状态
   */
  getInitializationStatus(): {
    isReady: boolean
    isInitializing: boolean
    queueLength: number
  } {
    return {
      isReady: this.isWasmReady,
      isInitializing: this.initializationPromise !== null && !this.isWasmReady,
      queueLength: this.messageQueue.length
    }
  }

  /**
   * 重置WASM管理器状态
   */
  reset(): void {
    this.isWasmReady = false
    this.messageQueue = []
    this.initializationPromise = null
  }

  /**
   * 销毁WASM管理器
   */
  async destroy(): Promise<void> {
    await this.releaseWasmModules()
    this.reset()
  }
}