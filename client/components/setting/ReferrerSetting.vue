<template>
  <div class="w-full px-2 bg-zinc-700/40 rounded-lg">
    <button @click="isExpanded = !isExpanded"
      class="flex items-center justify-between w-full p-2 text-left text-primary font-medium rounded-lg">
      <div>
        <h2 class="text-lg font-bold text-zinc-200">{{ $t('referrerManagement.title') }}</h2>
        <p class="text-muted-foreground">{{ $t('referrerManagement.description') }}</p>
      </div>
      <div class="mr-2">
        <Icon v-if="isExpanded" icon="lucide:chevrons-up" class="mr-2 h-4 w-4" />
        <Icon v-else icon="lucide:chevrons-down" class="mr-2 h-4 w-4" />
      </div>
    </button>
    <div v-if="isExpanded" class="space-y-6 px-2 py-2 mb-2">
      <div class="text-red-400 flex" v-if="referrerNames.length">
          <span class="text-sm ">已注册推荐人：</span>
          <ul class="text-sm flex gap-2 flw-wrap">
            <li v-for="n in referrerNames" :key="n">{{ n }}</li>
          </ul>
      </div>
      <div class="flex justify-center gap-3 mb-2">
        <Button as-child variant="secondary" class="h-10 w-32">
          <RouterLink to="/wallet/setting/referrer/register" class="w-full">
            {{ $t('referrerManagement.registerAsReferrer') }}
          </RouterLink>
        </Button>
        <Button as-child class="h-10 w-32">
          <RouterLink to="/wallet/setting/referrer/bind" class="w-full">
            {{ $t('referrerManagement.bindReferrer') }}
          </RouterLink>
        </Button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, onMounted } from 'vue'
import { Button } from '@/components/ui/button'
import { useWalletStore } from '@/store/wallet'
import { storeToRefs } from 'pinia'
import { storage } from 'wxt/storage'

const isExpanded = ref(false)
const walletStore = useWalletStore()
const { address } = storeToRefs(walletStore)
const referrerNames = ref<string[]>([])

async function loadReferrerNames() {
  if (address.value) {
    const key = `local:referrer_names_${address.value}` as const
    const names = await storage.getItem<string[]>(key)
    referrerNames.value = names || []
  } else {
    referrerNames.value = []
  }
  console.log(referrerNames.value);
  
}

watch([address, isExpanded], ([addr, expanded]) => {
  if (expanded) loadReferrerNames()
})
onMounted(() => {
  if (isExpanded.value) loadReferrerNames()
})
</script>