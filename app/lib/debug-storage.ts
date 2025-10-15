/**
 * 调试存储问题的工具函数
 */
import { Storage } from './storage-adapter'
import { walletStorage } from './walletStorage'

export async function debugStorage() {
  console.log('=== Storage Debug ===')
  
  // 检查 localStorage 中的所有钱包相关数据
  console.log('1. localStorage 中的所有钱包相关数据:')
  for (let i = 0; i < localStorage.length; i++) {
    const key = localStorage.key(i)
    if (key && (key.includes('wallet') || key.includes('password'))) {
      const value = localStorage.getItem(key)
      console.log(`  ${key}: ${value}`)
    }
  }
  
  // 测试通过适配器读取密码
  console.log('\n2. 通过适配器读取关键数据:')
  const passwordResult = await Storage.get({ key: 'local:wallet_password' })
  console.log('  local:wallet_password:', passwordResult.value)
  
  const passwordTimeResult = await Storage.get({ key: 'local:wallet_passwordTime' })
  console.log('  local:wallet_passwordTime:', passwordTimeResult.value)
  
  const hasWalletResult = await Storage.get({ key: 'local:wallet_hasWallet' })
  console.log('  local:wallet_hasWallet:', hasWalletResult.value)
  
  const lockedResult = await Storage.get({ key: 'local:wallet_locked' })
  console.log('  local:wallet_locked:', lockedResult.value)
  
  const networkResult = await Storage.get({ key: 'local:wallet_network' })
  console.log('  local:wallet_network:', networkResult.value)
  
  // 测试 walletStorage 初始化前后的状态
  console.log('\n3. walletStorage 状态测试:')
  console.log('  初始化前 - password:', walletStorage.getValue('password'))
  console.log('  初始化前 - passwordTime:', walletStorage.getValue('passwordTime'))
  
  await walletStorage.initializeState()
  
  console.log('  初始化后 - password:', walletStorage.getValue('password'))
  console.log('  初始化后 - passwordTime:', walletStorage.getValue('passwordTime'))
  console.log('  初始化后 - hasWallet:', walletStorage.getValue('hasWallet'))
  console.log('  初始化后 - locked:', walletStorage.getValue('locked'))
  
  // 检查密码时效性
  const passwordTime = walletStorage.getValue('passwordTime')
  if (passwordTime) {
    const now = new Date().getTime()
    const timeDiff = now - passwordTime
    console.log(`  密码时间差: ${timeDiff}ms (${timeDiff / 1000 / 60} 分钟)`)
    console.log(`  密码是否过期: ${timeDiff > 5 * 60 * 1000}`)
  }
  
  console.log('=== End Debug ===')
}

// 在浏览器控制台中可以调用
if (typeof window !== 'undefined') {
  (window as any).debugStorage = debugStorage
}
