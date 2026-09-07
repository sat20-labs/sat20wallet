import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'
import path from 'path'
import fs from 'node:fs'
import crypto from 'node:crypto'

const readVersionInfo = () => {
  try {
    return JSON.parse(fs.readFileSync(path.resolve(__dirname, 'public/version.json'), 'utf8'))
  } catch {
    return {}
  }
}

const collectBuildAssets = (dir: string, distRoot: string): string[] => {
  if (!fs.existsSync(dir)) return []
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const absolute = path.join(dir, entry.name)
    if (entry.isDirectory()) return collectBuildAssets(absolute, distRoot)
    if (!/\.(?:js|css)$/.test(entry.name)) return []
    return [`/${path.relative(distRoot, absolute).split(path.sep).join('/')}`]
  })
}

const collectFiles = (dir: string, distRoot: string): string[] => {
  if (!fs.existsSync(dir)) return []
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const absolute = path.join(dir, entry.name)
    if (entry.isDirectory()) return collectFiles(absolute, distRoot)
    return [`/${path.relative(distRoot, absolute).split(path.sep).join('/')}`]
  })
}

const sha256File = (file: string) => crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex')

const mimeForPath = (assetPath: string) => {
  if (assetPath.endsWith('.wasm')) return 'application/wasm'
  if (assetPath.endsWith('.js')) return 'text/javascript'
  if (assetPath.endsWith('.css')) return 'text/css'
  if (assetPath.endsWith('.html')) return 'text/html'
  throw new Error(`No release MIME policy for ${assetPath}`)
}

const releaseBuildPlugin = (versionInfo: Record<string, any>) => ({
  name: 'sat20-pwa-release',
  closeBundle() {
    const distRoot = path.resolve(__dirname, 'dist')
    const serviceWorkerPath = path.join(distRoot, 'service-worker.js')
    const assetMarker = '/* __SAT20_BUILD_ASSETS__ */ []'
    const releaseMarker = '/* __SAT20_RELEASE_MANIFEST__ */ {}'
    const buildAssets = collectBuildAssets(path.join(distRoot, 'assets'), distRoot).sort()
    const expectedReleaseId = `${versionInfo.version}-${versionInfo.buildId}`
    const wasmManifest = JSON.parse(
      fs.readFileSync(path.join(distRoot, 'integrity-manifest.json'), 'utf8')
    )

    if (wasmManifest.releaseId !== expectedReleaseId) {
      throw new Error(`WASM integrity release ${wasmManifest.releaseId} does not match ${expectedReleaseId}`)
    }
    if (!buildAssets.some((asset) => asset.endsWith('.js')) ||
        !buildAssets.some((asset) => asset.endsWith('.css'))) {
      throw new Error(`PWA release expected emitted JS and CSS assets: ${buildAssets.join(', ')}`)
    }

    const criticalPaths = [
      '/index.html',
      ...buildAssets,
      ...wasmManifest.assets.map((asset: { path: string }) => `/${asset.path}`),
    ]
    const assets = criticalPaths.map((assetPath) => {
      const file = path.join(distRoot, assetPath.replace(/^\//, ''))
      if (!fs.existsSync(file)) throw new Error(`PWA release asset is missing: ${assetPath}`)
      return {
        path: assetPath,
        mime: mimeForPath(assetPath),
        size: fs.statSync(file).size,
        sha256: sha256File(file),
      }
    })
    for (const expected of wasmManifest.assets) {
      const actual = assets.find((asset) => asset.path === `/${expected.path}`)
      if (!actual || actual.sha256 !== expected.sha256 || actual.size !== expected.size) {
        throw new Error(`Built ${expected.path} does not match generated integrity metadata`)
      }
    }

    const forbiddenArtifacts = collectFiles(distRoot, distRoot)
      .filter((asset) => /(?:\.map|\.symbols|\.debug)$/i.test(asset))
    if (forbiddenArtifacts.length) {
      throw new Error(`Public build contains internal symbol artifacts: ${forbiddenArtifacts.join(', ')}`)
    }

    const releaseManifest = {
      schemaVersion: 1,
      releaseId: expectedReleaseId,
      version: versionInfo.version,
      buildId: versionInfo.buildId,
      commit: versionInfo.commit || '',
      publishedAt: versionInfo.publishedAt || '',
      serviceWorker: versionInfo.serviceWorker || {},
      assets,
    }
    fs.writeFileSync(
      path.join(distRoot, 'release-manifest.json'),
      `${JSON.stringify(releaseManifest, null, 2)}\n`
    )

    const serviceWorker = fs.readFileSync(serviceWorkerPath, 'utf8')
    if (!serviceWorker.includes(assetMarker) || !serviceWorker.includes(releaseMarker)) {
      throw new Error('PWA service worker release markers are missing')
    }
    fs.writeFileSync(
      serviceWorkerPath,
      serviceWorker
        .replace(assetMarker, JSON.stringify(buildAssets, null, 2))
        .replace(releaseMarker, JSON.stringify(releaseManifest, null, 2))
    )
  },
})

const productionOrigin = (value: string, directive: 'frame-src' | 'connect-src') => {
  const parsed = new URL(value)
  const allowedSchemes = directive === 'connect-src' ? ['https:', 'wss:'] : ['https:']
  const hostname = parsed.hostname.toLowerCase()
  if (!allowedSchemes.includes(parsed.protocol) ||
      hostname === 'localhost' || hostname === '127.0.0.1' || hostname === '::1') {
    throw new Error(`Production CSP rejects ${directive} origin: ${value}`)
  }
  return parsed.origin
}

const configuredOrigins = (values: Array<string | undefined>, directive: 'frame-src' | 'connect-src') => {
  const tokens = values.flatMap((value) => (value || '').split(/[\s,]+/)).filter(Boolean)
  return Array.from(new Set(tokens.map((value) => productionOrigin(value, directive)))).sort()
}

const productionCspPlugin = (env: Record<string, string>, releaseId: string) => {
  const frameOrigins = configuredOrigins([
    'https://app.ordx.market',
    'https://satsnet.ordx.market',
    'https://test-satsnet.ordx.market',
    env.VITE_SAT20_MARKET_URL,
    env.VITE_SAT20_DAPP_ALLOWED_ORIGINS,
    env.VITE_CSP_FRAME_SRC,
  ], 'frame-src')
  const connectOrigins = configuredOrigins([
    'https://apiprd.sat20.org',
    'https://apiprd.ordx.market',
    env.VITE_SAT20_VERSION_URL,
    env.VITE_CSP_CONNECT_SRC,
  ], 'connect-src')
  const content = [
    "default-src 'self'",
    "base-uri 'none'",
    "object-src 'none'",
    "script-src 'self' blob: 'wasm-unsafe-eval'",
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data: blob:",
    "font-src 'self' data:",
    `frame-src 'self' ${frameOrigins.join(' ')}`,
    `connect-src 'self' ${connectOrigins.join(' ')}`,
    "worker-src 'self' blob:",
    "manifest-src 'self'",
    "form-action 'self'",
    "frame-ancestors 'none'",
    'upgrade-insecure-requests',
  ].join('; ')

  return {
    name: 'sat20-production-csp',
    transformIndexHtml(html: string) {
      return {
        html,
        tags: [
          { tag: 'meta', attrs: { 'http-equiv': 'Content-Security-Policy', content }, injectTo: 'head-prepend' as const },
          { tag: 'meta', attrs: { name: 'sat20-release', content: releaseId }, injectTo: 'head-prepend' as const },
        ],
      }
    },
  }
}

// soljson.js is an Emscripten universal build. Its Node-only branch contains
// static require("path") / require("fs") calls even though that branch is never
// entered in browsers. Resolve only those two imports, and only for soljson,
// so Vite does not externalize Node modules or affect unrelated dependencies.
const solcBrowserNodeShims = () => {
  const virtualID = '\0sat20-solc-node-shim'
  return {
    name: 'sat20-solc-browser-node-shims',
    enforce: 'pre' as const,
    resolveId(source: string, importer?: string) {
      const normalizedImporter = importer?.split(path.sep).join('/') || ''
      if ((source === 'path' || source === 'fs') &&
          normalizedImporter.endsWith('/node_modules/solc/soljson.js')) {
        return virtualID
      }
      return null
    },
    load(id: string) {
      if (id !== virtualID) return null
      return `
        const unavailable = () => { throw new Error('Node filesystem APIs are unavailable in the PWA') }
        export const dirname = (value) => value
        export const normalize = (value) => value
        export const readFileSync = unavailable
        export const readFile = unavailable
        export default { dirname, normalize, readFileSync, readFile }
      `
    },
  }
}

// https://vitejs.dev/config/
export default defineConfig(({ command, mode }) => {
  const versionInfo = readVersionInfo()
  const env = loadEnv(mode, __dirname, '')
  const releaseId = `${versionInfo.version || '0.0.0'}-${versionInfo.buildId || ''}`
  const productionBuild = command === 'build' && mode === 'production'

  return {
    base: process.env.VITE_PWA_BASE_PATH || (mode === 'production' ? '/pwa/' : '/'),
    define: {
      __SAT20_APP_VERSION__: JSON.stringify(versionInfo.version || '0.0.0'),
      __SAT20_BUILD_ID__: JSON.stringify(versionInfo.buildId || ''),
    },
    plugins: [
      solcBrowserNodeShims(),
      vue(),
      ...(productionBuild ? [productionCspPlugin(env, releaseId)] : []),
      releaseBuildPlugin(versionInfo),
    ],
    build: {
      sourcemap: false,
    },
    server: {
      port: 5173,
      strictPort: true,
    },
    preview: {
      port: 4173,
      strictPort: true,
    },
    resolve: {
      alias: [
        // The PWA only needs local standard-JSON compilation. Avoid the
        // Node-oriented solc wrapper (remote loader, http/stream/fs helpers)
        // and keep the compiler in the lazy Tools chunk.
        { find: /^solc$/, replacement: path.resolve(__dirname, './utils/solc-browser.ts') },
        { find: '@', replacement: path.resolve(__dirname, './') },
        { find: '~', replacement: path.resolve(__dirname, './') },
      ],
    },
  }
})
