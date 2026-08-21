import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import path from 'path'
import fs from 'node:fs'

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

const injectPwaPrecache = () => ({
  name: 'sat20-pwa-precache',
  closeBundle() {
    const distRoot = path.resolve(__dirname, 'dist')
    const serviceWorkerPath = path.join(distRoot, 'service-worker.js')
    const marker = '/* __SAT20_BUILD_ASSETS__ */ []'
    const assets = collectBuildAssets(path.join(distRoot, 'assets'), distRoot).sort()

    if (!assets.some((asset) => asset.endsWith('.js')) ||
        !assets.some((asset) => asset.endsWith('.css'))) {
      throw new Error(`PWA precache expected emitted JS and CSS assets: ${assets.join(', ')}`)
    }
    const serviceWorker = fs.readFileSync(serviceWorkerPath, 'utf8')
    if (!serviceWorker.includes(marker)) {
      throw new Error('PWA service worker precache marker is missing')
    }
    fs.writeFileSync(
      serviceWorkerPath,
      serviceWorker.replace(marker, JSON.stringify(assets, null, 2))
    )
  },
})

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
export default defineConfig(({ mode }) => {
  const versionInfo = readVersionInfo()

  return {
    base: process.env.VITE_PWA_BASE_PATH || (mode === 'production' ? '/pwa/' : '/'),
    define: {
      __SAT20_APP_VERSION__: JSON.stringify(versionInfo.version || '0.0.0'),
      __SAT20_BUILD_ID__: JSON.stringify(versionInfo.buildId || ''),
    },
    plugins: [solcBrowserNodeShims(), vue(), injectPwaPrecache()],
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
