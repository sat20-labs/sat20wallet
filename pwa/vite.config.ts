import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import path from 'path'

// https://vitejs.dev/config/
export default defineConfig(({ mode }) => ({
  base: process.env.VITE_PWA_BASE_PATH || (mode === 'production' ? '/pwa/' : '/'),
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
}))
