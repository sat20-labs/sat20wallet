<template>
  <div class="fixed inset-0 flex items-center justify-center bg-black bg-opacity-50 z-50">
    <div class="w-96 bg-zinc-900 rounded-lg shadow-lg p-6 relative">
      <!-- Close Button -->
      <button
        class="absolute top-4 right-4 text-zinc-400 hover:text-zinc-300"
        @click="$emit('close')"
      >
        ✕
      </button>
      <!-- Dialog Header -->
      <div class="text-center">
        <h2 class="text-lg font-semibold text-white">{{ $t('receiveQRcode.title') }}</h2>
      </div>
      <!-- QR Code and Address -->
      <div class="flex flex-col items-center space-y-4 py-4">
        <canvas ref="qrCanvas" class="w-64 h-64 bg-white rounded-lg shadow-md"></canvas>
        <div class="text-center">
          <p class="flex justify-center text-sm text-zinc-400 mb-2">
            <Icon icon="lucide:user-round" class="w-5 h-5 text-purple-600 flex-shrink-0" />
            {{ $t('receiveQRcode.account') }}
          </p>
          <p class="text-sm text-zinc-300 font-mono">{{ hideAddress(address, 8) }}</p>
        </div>
        <button
          class="flex justify-center w-full text-zinc-400 border border-zinc-600 hover:bg-zinc-700 py-2 rounded-lg"
          @click="copyAddress"
        >
          <Icon icon="solar:copy-linear" class="text-zinc-400 w-4 h-4 ml-2" />
          {{ $t('receiveQRcode.copyAddress') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, onMounted } from 'vue'
import { hideAddress } from '~/utils'
import QRCode from 'qrcode'
import { toast } from '@/components/ui/toast-new'


// Props for address and chain
const { address, chain } = defineProps<{
  address: string
  chain: string
}>()

const qrCanvas = ref<HTMLCanvasElement | null>(null)

// Generate QR Code
const generateQRCode = async () => {
  if (qrCanvas.value) {
    // Generate the QR code
    await QRCode.toCanvas(qrCanvas.value, address, {
      width: 256, // Larger size for better visibility
      margin: 4, // Add margin for better readability
      color: {
        dark: '#d822ff', // QR code color
        light: '#f1f1f1', // Background color
      },
    })

    // Add dynamic icon to the center of the QR code
    const canvas = qrCanvas.value
    const ctx = canvas.getContext('2d')
    if (ctx) {
      const icon = new Image()
      icon.src = currentIcon.value.imagePath // Use the dynamic icon path
      console.log('Icon path:', icon.src) // Debugging line to check the icon path
      icon.onload = () => {
        const iconSize = canvas.width * 0.2 // Icon size is 20% of the QR code size
        const iconX = (canvas.width - iconSize) / 2
        const iconY = (canvas.height - iconSize) / 2

        // Draw a background circle
        const circleRadius = iconSize / 2 + 4 // Add padding around the icon
        ctx.beginPath()
        ctx.arc(canvas.width / 2, canvas.height / 2, circleRadius, 0, 2 * Math.PI)
        ctx.fillStyle = '#FFFFFF' // Background circle color
        ctx.fill()

        // Draw the icon
        ctx.drawImage(icon, iconX, iconY, iconSize, iconSize)
      }
    }
  }
}

const currentIcon = computed(() => {
  const icons: Record<string, { imagePath: string }> = {
    bitcoin: {
      // icon: 'cryptocurrency:btc',
      // iconColor: 'text-orange-500',
      imagePath: '/icon/testnet.svg', // Replace with actual image path
    },
    satoshinet: {
      // icon: 'cryptocurrency:btc',
      // iconColor: 'text-green-500',
      imagePath: '/icon/satoshinet.svg', // Replace with actual image path
    },
  }
  return icons[chain.toLowerCase()] || icons['bitcoin']
})


// Copy Address to Clipboard
const copyAddress = async () => {
  try {
    await navigator.clipboard.writeText(address)
    toast({
      title: 'Copied to clipboard',
      description: 'Address copied successfully!',
      variant: 'success'
    })
    
  } catch (err) {
    console.error('Failed to copy address:', err)
  }
}

// Generate QR code on mount
onMounted(() => {
  generateQRCode()
})
</script>

<style scoped>
.bg-zinc-900 {
  background-color: #1a1a1a;
}

.shadow-lg {
  box-shadow: 0 4px 6px rgba(0, 0, 0, 0.1), 0 1px 3px rgba(0, 0, 0, 0.06);
}

button:hover {
  cursor: pointer;
}

canvas {
  border-radius: 8px;
}
</style>