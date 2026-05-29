/**
 * 重启 PWA。浏览器环境不能像原生 App 一样退出进程，只能刷新当前 shell。
 */
export const restartApp = () => {
  console.log('Reloading PWA shell')
  setTimeout(() => {
    window.location.reload()
  }, 600)
}
