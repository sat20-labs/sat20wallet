import { defineConfig } from 'wxt'

// See https://wxt.dev/api/config.html
export default defineConfig({
  extensionApi: 'chrome',
  modules: ['@wxt-dev/module-vue'],
  manifest: {
    name: 'sat20wallet',
    web_accessible_resources: [
      {
        resources: ['injected.js'],
        matches: ['*://*/*'],
      },
      {
        resources: ['sat20wallet.wasm'],
        matches: ['*://*/*'],
      },
    ],
    content_security_policy: {
      extension_pages: "script-src 'self' 'wasm-unsafe-eval'; object-src 'self';"
    },
    permissions: ['tabs', 'storage', 'activeTab'],
  },
  runner: {
    startUrls: ['http://localhost:3001/test.html'],
  },
  imports: {
    presets: ['pinia', 'vue-router', '@vueuse/core', 'date-fns'],
  },
  vite: () => ({
    // Override config here, same as `defineConfig({ ... })`
    // inside vite.config.ts files
    // plugins: [tailwindcss()],
  }),
})
