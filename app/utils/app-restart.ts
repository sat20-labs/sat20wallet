import { App } from '@capacitor/app'

/**
 * 重启应用 - 使用 App.exitApp() 方法
 * 在原生环境中退出应用，系统会自动重启
 */
export const restartApp = () => {
  console.log('使用 App.exitApp() 重启应用')
  setTimeout(() => {
    App.exitApp().then(() => {
      console.log('应用已退出，系统会自动重启')
    }).catch((error) => {
      console.error('Failed to restart app:', error)
    })
  }, 600)
}