<template>
  <LayoutHome>
    <WalletHeader />
    <div class="mx-auto max-w-3xl space-y-5 pb-6">
      <h1 class="text-2xl font-semibold">{{ $t('setting.title') }}</h1>
      <section class="space-y-2" aria-labelledby="settings-account">
        <h2 id="settings-account" class="text-sm font-semibold text-muted-foreground">{{ $t('pwaUi.design.account') }}</h2>
        <AccountManagementSetting />
        <Button variant="outline" class="min-h-11 w-full justify-between" @click="$router.push('/wallet/name-select')">
          {{ $t('pwaUi.settings.did') }} <ChevronRight class="size-4" aria-hidden="true" />
        </Button>
      </section>
      <section class="space-y-2" aria-labelledby="settings-security">
        <h2 id="settings-security" class="text-sm font-semibold text-muted-foreground">{{ $t('pwaUi.design.security') }}</h2>
        <SecuritySetting />
        <EscapeHatch v-if="transcendingModeStore.selectedTranscendingMode !== 'poolswap'" />
      </section>
      <section class="space-y-2" aria-labelledby="settings-network">
        <h2 id="settings-network" class="text-sm font-semibold text-muted-foreground">{{ $t('pwaUi.design.network') }}</h2>
        <NetworkSetting />
        <NodeSetting />
        <ReferrerSetting />
      </section>
      <section class="space-y-2" aria-labelledby="settings-advanced">
        <h2 id="settings-advanced" class="text-sm font-semibold text-muted-foreground">{{ $t('pwaUi.design.advanced') }}</h2>
        <Button variant="outline" class="min-h-11 w-full justify-between" @click="$router.push('/wallet/setting/operation-logs')">
          {{ $t('pwaUi.settings.logs') }} <ChevronRight class="size-4" aria-hidden="true" />
        </Button>
        <Button variant="outline" class="min-h-11 w-full justify-between" @click="$router.push('/wallet/setting/utxo')">
          {{ $t('utxoManager.title') }} <ChevronRight class="size-4" aria-hidden="true" />
        </Button>
      </section>
    </div>

    <div class="flex flex-col items-center gap-4 pb-4">
      <div class="flex items-center gap-4">
        <a href="https://twitter.com/sat20labs" target="_blank" rel="noopener noreferrer" aria-label="SAT20 Labs on X" class="text-muted-foreground hover:text-primary">
          <Icon icon="bi:twitter-x" class="w-4 h-4" />
        </a>
        <a href="https://github.com/sat20-labs/" target="_blank" rel="noopener noreferrer" aria-label="SAT20 Labs on GitHub" class="text-muted-foreground hover:text-primary">
          <Icon icon="bi:github" class="w-4 h-4" />
        </a>
      </div>
      <div class="flex flex-wrap items-center justify-center gap-2 text-sm text-muted-foreground">
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
          {{ isForceUpdate ? $t('setting.versionWriteBlocked') : '' }}
          {{ isChecking ? $t('setting.checking') : (isUpdating ? $t('setting.updating') : $t('setting.checkAndUpdate')) }}
        </Button>
      </div>
      <div class="space-y-1 text-xs text-muted-foreground/80">
        <div>sat20wallet.wasm: {{ sat20WasmVersion }}</div>
      </div>
    </div>
  </LayoutHome>
</template>

<script setup lang="ts">
import LayoutHome from '@/components/layout/LayoutHome.vue'
import WalletHeader from '@/components/wallet/HomeHeader.vue'
import SecuritySetting from '@/components/setting/SecuritySetting.vue'
import AccountManagementSetting from '@/components/account/AccountManagementSetting.vue'
import EscapeHatch from '@/components/setting/EscapeHatch.vue'
import NetworkSetting from '@/components/setting/NetworkSetting.vue'
import NodeSetting from '@/components/setting/NodeSetting.vue'
import ReferrerSetting from '@/components/setting/ReferrerSetting.vue'
import { Button } from '@/components/ui/button'
import { ChevronRight } from 'lucide-vue-next'
import { Icon } from '@iconify/vue'
import { computed, onMounted, ref } from 'vue'
import { useTranscendingModeStore } from '@/store'
import { useAppVersion } from '@/composables/useAppVersion'
import sat20 from '@/utils/sat20'

const transcendingModeStore = useTranscendingModeStore()
const { isChecking, isUpdating, isForceUpdate, checkAndUpdate, localVersion, localBuildId } = useAppVersion()
const versionDisplay = computed(() => localBuildId.value ? `${localVersion.value}+${localBuildId.value}` : localVersion.value)
const sat20WasmVersion = ref('-')

onMounted(async () => {
  const [sat20Err, sat20Version] = await sat20.getVersion()
  sat20WasmVersion.value = sat20Err ? 'unavailable' : (sat20Version?.version || '-')
})
</script>
