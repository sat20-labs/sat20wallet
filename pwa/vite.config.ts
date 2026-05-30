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

// https://vitejs.dev/config/
export default defineConfig(({ mode }) => {
  const versionInfo = readVersionInfo()

  return {
    base: process.env.VITE_PWA_BASE_PATH || (mode === 'production' ? '/pwa/' : '/'),
    define: {
      __SAT20_APP_VERSION__: JSON.stringify(versionInfo.version || '0.0.0'),
      __SAT20_BUILD_ID__: JSON.stringify(versionInfo.buildId || ''),
    },
    plugins: [vue()],
    server: {
      port: 5173,
      strictPort: true,
    },
    preview: {
      port: 4173,
      strictPort: true,
    },
    resolve: {
      alias: {
        '@': path.resolve(__dirname, './'),
        '~': path.resolve(__dirname, './'),
      },
    },
  }
})
