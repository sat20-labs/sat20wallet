<template>
  <div v-if="isOpen" class="space-y-4 h-64 w-full">
    <h2 class="text-xl text-center font-sans font-bold text-zinc-400">{{ $t('transcendingMode.title') }}</h2>
    <div class="flex flex-col gap-4 text-base font-semibold text-zinc-400 items-start py-2 border-t border-zinc-950/30">
      {{ $t('transcendingMode.fastMode') }}
      <Button 
        :variant="transcendingModeStore.selectedTranscendingMode === 'poolswap' ? 'secondary' : 'outline'" 
        @click="selectMode('poolswap')" 
        class="w-full h-12 justify-start gap-2 text-base bg-zinc-800 hover:bg-zinc-700/50"
      >
        <Icon icon="material-icon-theme:hurl" class="w-8 h-8 shrink-0" />{{ $t('transcendingMode.poolswap') }}
      </Button>
      {{ $t('transcendingMode.advancedMode') }}
      <Button 
        :variant="transcendingModeStore.selectedTranscendingMode === 'lightning' ? 'secondary' : 'outline'" 
        @click="selectMode('lightning')" 
        :disabled="!canUseLightning"
        class="w-full h-12 justify-start gap-2 text-base bg-zinc-800 hover:bg-zinc-700/50 "
      >
        <Icon icon="material-icon-theme:supabase" class="w-6 h-6 shrink-0" />{{ $t('transcendingMode.lightning') }}
      </Button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { Icon } from '@iconify/vue'
import { Button } from '@/components/ui/button'
import { useTranscendingModeStore } from '@/store'
import { useWalletStore } from '@/store/wallet'
import { WalletType } from '@/types'
import { storeToRefs } from 'pinia'

const transcendingModeStore = useTranscendingModeStore()
const walletStore = useWalletStore()
const { currentWalletType } = storeToRefs(walletStore)

const isOpen = ref(true) // 控制弹出窗口的打开状态

const emit = defineEmits(['close']); // 声明 close 事件

// 只有助记词钱包才能使用 lightning 模式
const canUseLightning = computed(() => currentWalletType.value === WalletType.MNEMONIC)

const selectMode = (mode: 'poolswap' | 'lightning') => {
  transcendingModeStore.setMode(mode); // 设置模式
  console.log('选择的模式:', mode);
  emit('close'); // 触发关闭事件
};

onMounted(() => {
  console.log('当前模式:', transcendingModeStore.selectedTranscendingMode)
})
</script>