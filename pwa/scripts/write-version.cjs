const fs = require('node:fs')
const path = require('node:path')
const { execSync } = require('node:child_process')

const root = path.resolve(__dirname, '..')
const packageJson = JSON.parse(fs.readFileSync(path.join(root, 'package.json'), 'utf8'))
const versionPath = path.join(root, 'public', 'version.json')

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
const releaseId = `${process.env.SAT20_PWA_VERSION || packageJson.version}-${buildId}`
const killSwitch = process.env.SAT20_PWA_SW_KILL_SWITCH === 'true'
const next = {
  version: process.env.SAT20_PWA_VERSION || packageJson.version,
  buildId,
  commit: process.env.SAT20_PWA_COMMIT || getGitCommit(),
  releaseNotes: process.env.SAT20_PWA_RELEASE_NOTES || existing.releaseNotes || 'PWA build update',
  forceUpdate: existing.forceUpdate === true,
  minVersion: existing.minVersion || '0.1.0',
  publishedAt: now.toISOString(),
  serviceWorker: {
    schemaVersion: 1,
    releaseId,
    killSwitch,
  },
}

fs.writeFileSync(versionPath, `${JSON.stringify(next, null, 2)}\n`)
console.log(`Wrote ${path.relative(root, versionPath)} version=${next.version} buildId=${next.buildId}`)
