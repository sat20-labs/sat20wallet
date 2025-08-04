<template>
  <Toaster 
    :duration="duration" 
    :position="position"
    :rich-colors="true"
    :close-button="true"
    :expand="true"
    :theme="theme"
    class="custom-toaster"
  />
</template>

<script setup lang="ts">
import { Toaster } from 'vue-sonner'

interface Props {
  duration?: number
  position?: 'top-left' | 'top-center' | 'top-right' | 'bottom-left' | 'bottom-center' | 'bottom-right'
  theme?: 'light' | 'dark' | 'system'
}

withDefaults(defineProps<Props>(), {
  duration: 3000,
  position: 'top-right',
  theme: 'dark' // 改为 dark 主题以匹配应用
})
</script>