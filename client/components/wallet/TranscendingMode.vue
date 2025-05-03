<template>
  <div class="space-y-4 h-64 w-full">
    <h2 class="text-xl text-center font-sans font-bold text-zinc-400">Transcending Mode</h2>
    <div class="flex flex-col gap-4 text-base font-semibold text-zinc-400 items-start py-2 border-t border-zinc-950/30">
      Fast Mode:
      <Button 
        :variant="transcendingModeStore.selectedTranscendingMode === 'poolswap' ? 'secondary' : 'outline'" 
        @click="transcendingModeStore.setMode('poolswap')" 
        class="w-full h-12 justify-start gap-2 text-base bg-zinc-800 hover:bg-zinc-700/50"
      >
        <Icon icon="material-icon-theme:hurl" class="w-8 h-8 shrink-0" />Poolswap
      </Button>
      Advanced Mode:
      <Button 
        :variant="transcendingModeStore.selectedTranscendingMode === 'lightning' ? 'secondary' : 'outline'" 
        @click="transcendingModeStore.setMode('lightning')" 
        class="w-full h-12 justify-start gap-2 text-base bg-zinc-800 hover:bg-zinc-700/50 "
      >
        <Icon icon="material-icon-theme:supabase" class="w-6 h-6 shrink-0" />Lightning
      </Button>
    </div>
  </div>
</template>


<script setup lang="ts">
import { ref } from 'vue'
import { Icon } from '@iconify/vue'
import { Label } from '@/components/ui/label'
import { Button } from '@/components/ui/button'
import { useTranscendingModeStore } from '@/store'

const transcendingModeStore = useTranscendingModeStore()
</script>