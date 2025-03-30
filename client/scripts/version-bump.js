import fs from 'fs'
import path from 'path'
import { fileURLToPath } from 'url'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)

const packagePath = path.resolve(__dirname, '../package.json')
const package_json = JSON.parse(fs.readFileSync(packagePath, 'utf8'))

// 将版本号的最后一位加1
const version = package_json.version
const versionParts = version.split('.')
versionParts[2] = parseInt(versionParts[2]) + 1
package_json.version = versionParts.join('.')

// 写入新的版本号
fs.writeFileSync(packagePath, JSON.stringify(package_json, null, 2))

console.log(`Version bumped to ${package_json.version}`) 