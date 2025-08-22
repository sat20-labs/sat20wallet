import { defineConfig } from 'wxt'

import { NodeGlobalsPolyfillPlugin } from '@esbuild-plugins/node-globals-polyfill'
import { NodeModulesPolyfillPlugin } from '@esbuild-plugins/node-modules-polyfill'
// See https://wxt.dev/api/config.html
export default defineConfig({
  extensionApi: 'chrome',
  modules: ['@wxt-dev/module-vue'],
  manifest: {
    name: 'SAT20 Wallet',
    web_accessible_resources: [
      {
        resources: ['injected.js'],
        matches: ['*://*/*'],
      },
      {
        resources: ['sat20wallet.wasm', 'stp.wasm'],
        matches: ['*://*/*'],
      },
    ],
    content_scripts: [
      {
        matches: ['*://*/*'],
        js: ['content-scripts/content.js'],
      },
    ],
    content_security_policy: {
      extension_pages:
        "script-src 'self' 'wasm-unsafe-eval'; object-src 'self';",
    },
    permissions: ['tabs', 'storage', 'activeTab', 'scripting'],
  },
  runner: {
    startUrls: ['http://localhost:3002/account'],
  },
  imports: {
    presets: ['pinia', 'vue-router', 'date-fns'],
  },
  zip: {
    name: 'sat20wallet',
  },
  vite: () => ({
    esbuild: {
      target: 'esnext',
      // drop:
        // process.env.NODE_ENV === 'production' ? ['console', 'debugger'] : [],
    },
    plugins: [],
    logLevel: 'info' as const,
    optimizeDeps: {
      esbuildOptions: {
        supported: { 'top-level-await': true },
        define: { global: 'globalThis' },
        plugins: [
          NodeGlobalsPolyfillPlugin({
            process: true, // fix nuxt3 process
            buffer: true,
          }) as any,
          NodeModulesPolyfillPlugin(),
        ],
      },
    },
  }),
})
