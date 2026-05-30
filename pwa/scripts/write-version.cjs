const fs = require('node:fs')
const path = require('node:path')
const { execSync } = require('node:child_process')

const root = path.resolve(__dirname, '..')
const packageJson = JSON.parse(fs.readFileSync(path.join(root, 'package.json'), 'utf8'))
const versionPath = path.join(root, 'public', 'version.json')
const serviceWorkerPath = path.join(root, 'public', 'service-worker.js')

const readExistingVersion = () => {
  try {
    return JSON.parse(fs.readFileSync(versionPath, 'utf8'))
  } catch {
    return {}
  }
}

const getGitCommit = () => {
  try {
    return execSync('git rev-parse --short=12 HEAD', {
      cwd: root,
      stdio: ['ignore', 'pipe', 'ignore'],
    }).toString().trim()
  } catch {
    return ''
  }
}

const now = new Date()
const buildId = process.env.SAT20_PWA_BUILD_ID ||
  now.toISOString().replace(/[-:]/g, '').replace(/\.\d{3}Z$/, 'Z')

const existing = readExistingVersion()
const next = {
  version: process.env.SAT20_PWA_VERSION || packageJson.version,
  buildId,
  commit: process.env.SAT20_PWA_COMMIT || getGitCommit(),
  releaseNotes: process.env.SAT20_PWA_RELEASE_NOTES || existing.releaseNotes || 'PWA build update',
  forceUpdate: existing.forceUpdate === true,
  minVersion: existing.minVersion || '0.1.0',
  publishedAt: now.toISOString(),
}

fs.writeFileSync(versionPath, `${JSON.stringify(next, null, 2)}\n`)
try {
  const cacheName = `sat20-wallet-pwa-v${next.version}-${next.buildId}`
  const serviceWorker = fs.readFileSync(serviceWorkerPath, 'utf8')
  const updatedServiceWorker = serviceWorker.replace(
    /const CACHE_NAME = 'sat20-wallet-pwa-[^']+'/,
    `const CACHE_NAME = '${cacheName}'`
  )
  fs.writeFileSync(serviceWorkerPath, updatedServiceWorker)
  console.log(`Wrote public/service-worker.js cache=${cacheName}`)
} catch (error) {
  console.warn(`Failed to update service worker cache name: ${error.message}`)
}
console.log(`Wrote ${path.relative(root, versionPath)} version=${next.version} buildId=${next.buildId}`)
