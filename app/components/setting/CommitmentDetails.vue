<template>
    <div>
      <!-- View Commitment TX Modal -->
      <div class="relative p-4 space-y-4 bg-zinc-800 rounded-lg">
        <button
            @click="$emit('close')"
            class="absolute top-0 right-0 text-zinc-400 hover:text-zinc-200 hover:bg-zinc-700 rounded-full p-1 transition duration-200"
        >
            <Icon icon="lucide:x" class="h-5 w-5" />
        </button>
        <h3 class="text-lg font-bold text-zinc-200">Commitment Transaction Details</h3>
        <table class="w-full text-sm text-left text-muted-foreground border-collapse border border-zinc-700">
          <thead>
            <tr class="bg-zinc-800 text-zinc-300">
              <th class="px-4 py-2 border border-zinc-700">Label</th>
              <th class="px-4 py-2 border border-zinc-700">Value Example</th>
            </tr>
          </thead>
          <tbody>
            <tr>
              <td class="px-4 py-2 border border-zinc-700">Commitment TX ID</td>
              <td class="px-4 py-2 border border-zinc-700">0x9fd3...a7ef</td>
            </tr>
            <tr>
              <td class="px-4 py-2 border border-zinc-700">Output Index (vout)</td>
              <td class="px-4 py-2 border border-zinc-700">1</td>
            </tr>
            <tr>
              <td class="px-4 py-2 border border-zinc-700">Your Output Amount</td>
              <td class="px-4 py-2 border border-zinc-700">0.018 BTC</td>
            </tr>
            <tr>
              <td class="px-4 py-2 border border-zinc-700">Script Type</td>
              <td class="px-4 py-2 border border-zinc-700">P2WPKH</td>
            </tr>
            <tr>
              <td class="px-4 py-2 border border-zinc-700">Timelock / CSV</td>
              <td class="px-4 py-2 border border-zinc-700">24 blocks (≈ 4 hrs)</td>
            </tr>
          </tbody>
        </table>
  
        <!-- View Raw TX Button -->
        <div>
          <Button @click="showRawTx = !showRawTx" class="w-full bg-blue-500 text-white">
            {{ showRawTx ? 'Hide Raw TX' : 'View Raw TX' }}
          </Button>
          <div v-if="showRawTx" class="mt-2 p-2 bg-zinc-800 rounded-lg text-sm text-zinc-300">
            <p class="break-all">010000000001abcdef...123456</p>
            <Button @click="copyRawTx" class="mt-2 bg-green-500 text-white">Copy Raw TX</Button>
          </div>
        </div>
  
        <!-- Recovery Safety Tips -->
        <div>
          <button
            @click="showSafetyTips = !showSafetyTips"
            class="flex items-center justify-between w-full text-left text-yellow-500 font-medium"
          >
            ⚠️ Recovery Safety Tips
            <span>{{ showSafetyTips ? '▲' : '▼' }}</span>
          </button>
          <div v-if="showSafetyTips" class="mt-2 p-2 bg-yellow-100 rounded-lg text-sm text-yellow-900">
            <ul class="list-disc pl-4">
              <li>Force close will lock your funds for a certain time period (usually 24 blocks).</li>
              <li>Make sure your node stays online to monitor the broadcast and sweep TX.</li>
              <li>
                If you have issues recovering funds, please refer to our
                <a href="/recovery-guide" class="text-blue-500 underline">Recovery Guide</a>.
              </li>
            </ul>
          </div>
        </div>
      </div>
    </div>
  </template>
  
  <script setup lang="ts">
  import { ref } from 'vue'
  import { Button } from '@/components/ui/button'
  import { Icon } from '@iconify/vue'
  
  const showRawTx = ref(false)
  const showSafetyTips = ref(false)
  
  const copyRawTx = () => {
    const rawTx = '010000000001abcdef...123456' // 示例原始交易
    navigator.clipboard.writeText(rawTx).then(() => {
    //   alert('Raw TX copied to clipboard!')
    })
  }
  </script>