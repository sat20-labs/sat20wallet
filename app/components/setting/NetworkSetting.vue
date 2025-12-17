<template>
  <div class="w-full px-2  bg-zinc-700/40 rounded-lg">
    <button @click="isExpanded = !isExpanded"
      class="flex items-center justify-between w-full p-2 text-left text-primary font-medium rounded-lg">
      <div>
        <h2 class="text-lg font-bold text-zinc-200">{{ $t('networkSetting.title') }}</h2>
        <p class="text-muted-foreground">{{ $t('networkSetting.subtitle') }}</p>
      </div>
      <div class="mr-2">
        <Icon v-if="isExpanded" icon="lucide:chevrons-up" class="mr-2 h-4 w-4" />
        <Icon v-else icon="lucide:chevrons-down" class="mr-2 h-4 w-4" />
      </div>
    </button>
    <div v-if="isExpanded" class="space-y-6 px-2 mt-4">
      <div class="flex items-center justify-between border-t border-zinc-900/30 pt-4">
        <div class="text-sm text-muted-foreground mb-4">
          {{ $t('networkSetting.environmentSwitch') }}
        </div>
        <Select v-model="computedEnv">
          <SelectTrigger class="max-w-[160px] bg-gray-900/30 mb-4">
            <SelectValue :placeholder="$t('networkSetting.selectEnvironment')" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="dev">{{ $t('networkSetting.development') }}</SelectItem>
            <SelectItem value="test">{{ $t('networkSetting.test') }}</SelectItem>
            <SelectItem value="prd">{{ $t('networkSetting.production') }}</SelectItem>
          </SelectContent>
        </Select>
      </div>
      <div class="flex items-center justify-between">
        <div class="text-sm text-muted-foreground mb-4">
          {{ $t('networkSetting.networkSwitch') }} 
        </div>
        <Select v-model="network" @update:model-value="(value: any) => walletStore.setNetwork(value)">
          <SelectTrigger class="max-w-[160px] bg-gray-900/30 mb-4">
            <SelectValue :placeholder="$t('networkSetting.selectNetwork')" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem :value="Network.LIVENET">{{ $t('networkSetting.mainnet') }}</SelectItem>
            <SelectItem :value="Network.TESTNET">{{ $t('networkSetting.testnet') }}</SelectItem>
          </SelectContent>
        </Select>
      </div>
      <div class="flex items-center justify-between">
        <div class="text-sm text-muted-foreground mb-4">
          {{ $t('networkSetting.languageSwitch') }}
        </div>
        <Select v-model="currentLanguage" @update:model-value="(value) => changeLanguage(value as Language | null)">
          <SelectTrigger class="max-w-[160px] bg-gray-900/30 mb-4">
            <SelectValue :placeholder="$t('networkSetting.selectLanguage')" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="en">English</SelectItem>
            <SelectItem value="zh">中文</SelectItem>
          </SelectContent>
        </Select>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { setLanguage } from '@/main'
import type { Language } from '@/types'
import { Network } from '@/types'
import { Label } from '@/components/ui/label'
import { Icon } from '@iconify/vue'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { useWalletStore } from '@/store/wallet'
import { storeToRefs } from 'pinia'
import { useGlobalStore, type Env } from '@/store/global'
import { restartApp } from '@/utils/app-restart'

const isExpanded = ref(false)
const globalStore = useGlobalStore()
const walletStore = useWalletStore()
const { network, } = storeToRefs(walletStore)

const computedEnv = computed<Env>({
  get: () => globalStore.env,
  set: async (newValue) => {
    await globalStore.setEnv(newValue)
    restartApp()
  }
})

const { locale } = useI18n()
const currentLanguage = ref(locale.value)

const changeLanguage = (lang: Language | null) => {
  if (lang) {
    setLanguage(lang) // 调用 setLanguage 更新语言
  }
}
</script>