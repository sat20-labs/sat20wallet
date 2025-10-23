import fs from 'fs'
import path from 'path'
import { fileURLToPath } from 'url'

// 兼容ESM的__dirname写法
const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)

// 获取dist目录路径（Capacitor 应用的构建输出）
const distDir = path.resolve(__dirname, '../dist')
const releaseDir = path.resolve(__dirname, '../release')

// 匹配Android APK文件
const apkPattern = /^SAT20-Wallet-.*\.apk$/

// 确保release目录存在
if (!fs.existsSync(releaseDir)) {
  fs.mkdirSync(releaseDir)
}

try {
  // 读取dist目录下所有文件
  const files = fs.readdirSync(distDir)

  // 查找APK文件（如果有构建的话）
  const apkFiles = files.filter(f => apkPattern.test(f))

  if (apkFiles.length > 0) {
    // 按修改时间排序，取最新的
    apkFiles.sort((a, b) => {
      const statA = fs.statSync(path.join(distDir, a))
      const statB = fs.statSync(path.join(distDir, b))
      return statB.mtime.getTime() - statA.mtime.getTime()
    })

    const latestApk = apkFiles[0]
    const srcPath = path.join(distDir, latestApk)
    const destPath = path.join(releaseDir, latestApk)

    // 拷贝最新的APK
    fs.copyFileSync(srcPath, destPath)
    console.log(`已将最新APK ${latestApk} 拷贝到 release/ 目录`)
  } else {
    console.log('未找到APK文件，这是正常的，因为APK需要通过Android Studio或Ionic CLI构建')
    console.log('如需构建APK，请运行：')
    console.log('  npm run ionic:build')
    console.log('  bun run sync')
    console.log('  然后在Android Studio中打开android/目录进行构建')
  }

  // 也可以复制HTML构建结果用于web预览
  if (fs.existsSync(path.join(distDir, 'index.html'))) {
    console.log('Web构建文件已准备就绪，可以通过 bun run preview 进行预览')
  }

} catch (error) {
  console.log('构建文件复制完成或无需复制')
  console.log('Capacitor应用主要通过Android Studio或Xcode进行构建和发布')
} 