import fs from 'fs'
import path from 'path'
import { fileURLToPath } from 'url'

// 兼容ESM的__dirname写法
const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)

// 获取.output目录路径
const outputDir = path.resolve(__dirname, '../.output')
const releaseDir = path.resolve(__dirname, '../.output')

// 匹配sat20wallet-*-chrome.zip文件
const zipPattern = /^sat20wallet-(\d+\.\d+\.\d+)-chrome\.zip$/

// 读取.output目录下所有文件
const files = fs.readdirSync(outputDir)

// 过滤出所有符合命名规则的zip包
const zipFiles = files.filter(f => zipPattern.test(f))

if (zipFiles.length === 0) {
  console.error('未找到任何sat20wallet-*-chrome.zip文件')
  process.exit(1)
}

// 按版本号排序，取最新
zipFiles.sort((a, b) => {
  const verA = a.match(zipPattern)[1].split('.').map(Number)
  const verB = b.match(zipPattern)[1].split('.').map(Number)
  for (let i = 0; i < 3; i++) {
    if (verA[i] !== verB[i]) return verB[i] - verA[i]
  }
  return 0
})

const latestZip = zipFiles[0]
const srcPath = path.join(outputDir, latestZip)
const destPath = path.join(releaseDir, 'sat20wallet-chrome.zip')
const destPathTest = path.join(releaseDir, 'sat20wallet-chrome-test.zip')

// 确保release目录存在
if (!fs.existsSync(releaseDir)) {
  fs.mkdirSync(releaseDir)
}

// 拷贝并重命名
fs.copyFileSync(srcPath, destPath)
fs.copyFileSync(srcPath, destPathTest)
console.log(`已将最新包 ${latestZip} 拷贝为 release/sat20wallet-chrome.zip 和 release/sat20wallet-chrome-test.zip`)
