import { createApp } from 'vue'
import App from './entrypoints/popup/App.vue'
import router from './router'
import { createPinia } from 'pinia'

// Initialize WASM
async function initializeWasm() {
  const go = new (window as any).Go();
  const wasmModule = await WebAssembly.instantiateStreaming(
    fetch('/wasm/sat20wallet.wasm'),
    go.importObject
  );
  go.run(wasmModule.instance);
}

initializeWasm().then(() => {
  const app = createApp(App)
  const pinia = createPinia()

  app.use(pinia)
  app.use(router)

  app.mount('#app')
});