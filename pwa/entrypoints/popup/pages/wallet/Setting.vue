<template>
  <LayoutHome>
    <WalletHeader />
    <h2 class="px-4 text-2xl font-medium text-zinc-600/90">{{ $t('setting.title') }}</h2>
    <div class="space-y-2 py-4 px-0">
      <!-- Conditionally render EscapeHatch -->
      <Button variant="secondary" class="w-full h-10 mt-2 border-gray-600/50 bg-zinc-700/40" @click="$router.push({ path: '/wallet/setting/utxo' })">
        <Icon icon="lucide:lock-keyhole-open" class="w-10 h-10 mr-1 text-green-500 font-bold"/> {{$t('utxoManager.title')}}
      </Button>
      <Separator />
      <EscapeHatch v-if="transcendingModeStore.selectedTranscendingMode !== 'poolswap'" />
      <Separator />
      <SecuritySetting />
      <Separator />
      <NodeSetting />
      <Separator />
      <ReferrerSetting />
      <Separator />
      <NetworkSetting />

    </div>

    <!-- <AppearanceSetting /> -->

    <div class="flex flex-col items-center gap-4 pb-4">
      <div class="flex items-center gap-4">
        <a href="https://twitter.com/sat20labs" target="_blank" class="text-muted-foreground hover:text-primary">
          <Icon icon="bi:twitter-x" class="w-4 h-4" />
        </a>
        <a href="https://github.com/sat20-labs/" target="_blank" class="text-muted-foreground hover:text-primary">
          <Icon icon="bi:github" class="w-4 h-4" />
        </a>
        <!-- <a href="https://t.me/ordxnals" target="_blank" class="text-muted-foreground hover:text-primary">
          <Icon icon="bi:telegram" class="w-4 h-4" />
        </a> -->
      </div>
      <div class="flex items-center gap-2 text-sm text-muted-foreground">
        <span>{{ $t('setting.version') }}: {{ versionDisplay }}</span>
        <Button
          variant="ghost"
          size="sm"
          class="h-7 px-2 text-xs text-muted-foreground"
          :disabled="isChecking || isUpdating"
          @click="checkAndUpdate"
        >
          <Icon
            :icon="isChecking || isUpdating ? 'lucide:loader' : 'lucide:refresh-cw'"
            class="h-3.5 w-3.5"
            :class="{ 'animate-spin': isChecking || isUpdating }"
          />
          {{ isChecking ? $t('setting.checking') : (isUpdating ? $t('setting.updating') : $t('setting.checkAndUpdate')) }}
        </Button>
      </div>
      <div class="space-y-1 text-xs text-muted-foreground/80">
        <div>sat20wallet.wasm: {{ sat20WasmVersion }}</div>
        <div>stpd.wasm: {{ stpWasmVersion }}</div>
      </div>
    </div>
  </LayoutHome>
</template>

<script setup lang="ts">
import LayoutHome from '@/components/layout/LayoutHome.vue'
import WalletHeader from '@/components/wallet/HomeHeader.vue'
import SecuritySetting from '@/components/setting/SecuritySetting.vue'
import EscapeHatch from '@/components/setting/EscapeHatch.vue'
import NetworkSetting from '@/components/setting/NetworkSetting.vue'
import NodeSetting from '@/components/setting/NodeSetting.vue'
import ReferrerSetting from '@/components/setting/ReferrerSetting.vue'

import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
import { Icon } from '@iconify/vue'
import { computed, onMounted, ref } from "vue";
import { useTranscendingModeStore } from '@/store'
import { useAppVersion } from '@/composables/useAppVersion'
import sat20 from '@/utils/sat20'
import stp from '@/utils/stp'

type TranscendingMode = 'poolswap' | 'lightning'
const selectedTranscendingMode = ref<TranscendingMode>('poolswap')

const transcendingModeStore = useTranscendingModeStore()

const isOpen = ref(false);

const { isChecking, isUpdating, checkAndUpdate, localVersion, localBuildId } = useAppVersion()
const versionDisplay = computed(() => localBuildId.value ? `${localVersion.value}+${localBuildId.value}` : localVersion.value)
const sat20WasmVersion = ref('-')
const stpWasmVersion = ref('-')

onMounted(async () => {
  const [sat20Err, sat20Version] = await sat20.getVersion()
  sat20WasmVersion.value = sat20Err ? 'unavailable' : (sat20Version?.version || '-')

  const [stpErr, stpVersion] = await stp.getVersion()
  stpWasmVersion.value = stpErr ? 'unavailable' : (stpVersion || '-')
})
</script>
