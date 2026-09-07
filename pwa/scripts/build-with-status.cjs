const fs = require('node:fs')
const path = require('node:path')
const { spawn } = require('node:child_process')

const root = path.resolve(__dirname, '..')
const profile = process.argv[2] || 'production'
if (!['production', 'root', 'skip-check'].includes(profile)) {
  throw new Error(`Unsupported PWA build profile: ${profile}`)
}

const statusDir = path.resolve(process.env.SAT20_PWA_BUILD_STATUS_DIR || path.join(root, '.build-status'))
fs.mkdirSync(statusDir, { recursive: true })
const started = new Date()
const runId = started.toISOString().replace(/[:.]/g, '-')
const logPath = path.join(statusDir, `${profile}-${runId}.log`)
const statusPath = path.join(statusDir, 'latest.json')
const logFd = fs.openSync(logPath, 'a')
let activeChild

const writeStatus = (status, exitCode = null, error = '') => {
  const ended = status === 'RUNNING' ? null : new Date()
  const payload = {
    status,
    profile,
    startedAt: started.toISOString(),
    endedAt: ended?.toISOString() || null,
    durationMs: ended ? ended.getTime() - started.getTime() : null,
    exitCode,
    logPath,
    error,
  }
  const temporary = `${statusPath}.${process.pid}.tmp`
  fs.writeFileSync(temporary, `${JSON.stringify(payload, null, 2)}\n`)
  fs.renameSync(temporary, statusPath)
}

const writeChunk = (stream, chunk) => {
  stream.write(chunk)
  fs.writeSync(logFd, chunk)
}

const run = (label, argv, env = process.env) => new Promise((resolve, reject) => {
  writeChunk(process.stdout, Buffer.from(`\n[${new Date().toISOString()}] ${label}\n`))
  activeChild = spawn(process.execPath, argv, { cwd: root, env, stdio: ['inherit', 'pipe', 'pipe'] })
  activeChild.stdout.on('data', (chunk) => writeChunk(process.stdout, chunk))
  activeChild.stderr.on('data', (chunk) => writeChunk(process.stderr, chunk))
  activeChild.once('error', reject)
  activeChild.once('close', (code, signal) => {
    activeChild = undefined
    if (code === 0) resolve()
    else reject(new Error(`${label} failed with ${signal ? `signal ${signal}` : `exit code ${code}`}`))
  })
})

const stop = (signal) => {
  if (activeChild && !activeChild.killed) activeChild.kill(signal)
  writeStatus('FAILED', 128, `build interrupted by ${signal}`)
  fs.closeSync(logFd)
  process.exit(128)
}

process.once('SIGINT', () => stop('SIGINT'))
process.once('SIGTERM', () => stop('SIGTERM'))

const main = async () => {
  writeStatus('RUNNING')
  try {
    await run('write version metadata', [path.join(root, 'scripts', 'write-version.cjs')])
    await run('write WASM integrity metadata', [path.join(root, 'scripts', 'write-integrity-manifest.cjs')])
    if (profile !== 'skip-check') {
      await run('typecheck', [path.join(root, 'node_modules', 'vue-tsc', 'bin', 'vue-tsc.js'), '--noEmit'])
    }
    const buildEnv = profile === 'root'
      ? { ...process.env, VITE_PWA_BASE_PATH: '/' }
      : process.env
    await run('vite production build', [path.join(root, 'node_modules', 'vite', 'bin', 'vite.js'), 'build'], buildEnv)
    writeStatus('PASSED', 0)
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    writeChunk(process.stderr, Buffer.from(`\n${message}\n`))
    writeStatus('FAILED', 1, message)
    process.exitCode = 1
  } finally {
    fs.closeSync(logFd)
  }
}

void main()
