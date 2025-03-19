import { createApp } from 'vue'
import '@/assets/index.css'
import './style.css'
import App from '@/entrypoints/popup/App.vue'
import { createPinia } from 'pinia'
import router from '@/entrypoints/popup/router'
import { walletStorage } from '@/lib/walletStorage'
import { VueQueryPlugin } from '@tanstack/vue-query'
import { Icon } from '@iconify/vue'
import { loadWasm } from '@/utils/wasm'

loadWasm().then(() => {
  walletStorage.initializeState().then(() => {
    createApp(App)
      .component('Icon', Icon)
      .use(VueQueryPlugin)
      .use(createPinia())
      .use(router)
      .mount('#app')
  })
})
