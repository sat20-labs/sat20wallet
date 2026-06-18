<template>
  <main class="min-h-screen bg-background p-4 text-foreground">
    <h1 class="mb-4 text-xl font-semibold">Agent Sign Data</h1>
    <textarea
      v-model="message"
      class="mb-3 h-36 w-full rounded border border-border bg-background p-2 text-sm"
      placeholder="Raw protocol JSON"
    />
    <Button class="mb-3" @click="sign">Sign Data</Button>
    <pre class="whitespace-pre-wrap break-all rounded border border-border p-3 text-xs">{{ output }}</pre>
  </main>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { Button } from '@/components/ui/button'
import walletManager from '@/utils/sat20'

const message = ref('')
const output = ref('')

const sign = async () => {
  const [err, res] = await walletManager.signData(message.value)
  if (err) {
    output.value = err.message
    return
  }
  output.value = JSON.stringify(res, null, 2)
}
</script>
