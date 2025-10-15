/**
 * 简单的存储适配器测试
 * 用于验证 localStorage 适配器是否正常工作
 */
import { Storage } from './storage-adapter'

export async function testStorageAdapter(): Promise<boolean> {
  try {
    console.log('Testing storage adapter...')
    
    // 测试 set 和 get
    const testKey = 'local:wallet_test'
    const testValue = JSON.stringify({ test: 'data', timestamp: Date.now() })
    
    await Storage.set({ key: testKey, value: testValue })
    console.log('✓ Storage.set() works')
    
    const result = await Storage.get({ key: testKey })
    if (result.value !== testValue) {
      throw new Error('Retrieved value does not match stored value')
    }
    console.log('✓ Storage.get() works')
    
    // 测试 remove
    await Storage.remove({ key: testKey })
    const removedResult = await Storage.get({ key: testKey })
    if (removedResult.value !== null) {
      throw new Error('Value was not properly removed')
    }
    console.log('✓ Storage.remove() works')
    
    // 测试 clear (创建一些测试数据然后清除)
    await Storage.set({ key: 'local:wallet_test1', value: 'test1' })
    await Storage.set({ key: 'local:wallet_test2', value: 'test2' })
    await Storage.set({ key: 'other_key', value: 'should_not_be_removed' })
    
    await Storage.clear()
    
    const test1Result = await Storage.get({ key: 'local:wallet_test1' })
    const test2Result = await Storage.get({ key: 'local:wallet_test2' })
    const otherResult = await Storage.get({ key: 'other_key' })
    
    if (test1Result.value !== null || test2Result.value !== null) {
      throw new Error('Wallet keys were not properly cleared')
    }
    
    if (otherResult.value !== 'should_not_be_removed') {
      throw new Error('Non-wallet keys were incorrectly cleared')
    }
    
    // 清理测试数据
    localStorage.removeItem('other_key')
    
    console.log('✓ Storage.clear() works')
    console.log('✅ All storage adapter tests passed!')
    
    return true
  } catch (error) {
    console.error('❌ Storage adapter test failed:', error)
    return false
  }
}

// 如果直接运行此文件，执行测试
if (typeof window !== 'undefined') {
  // 在浏览器环境中可以直接调用
  // testStorageAdapter()
}
