import fs from 'fs'
import path from 'path'
import { fileURLToPath } from 'url'

// 兼容ESM的__dirname写法
const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)

// 获取.output目录路径
const outputDir = path.resolve(__dirname, '../.output')

if (!fs.existsSync(outputDir)) {
  console.log('.output目录不存在，无需清理')
  process.exit(0)
}

// 递归删除目录下所有内容
function emptyDir(dir) {
  for (const file of fs.readdirSync(dir)) {
    const fullPath = path.join(dir, file)
    if (fs.lstatSync(fullPath).isDirectory()) {
      emptyDir(fullPath)
      fs.rmdirSync(fullPath)
    } else {
      fs.unlinkSync(fullPath)
    }
  }
}

emptyDir(outputDir)
console.log('.output目录已清空') 