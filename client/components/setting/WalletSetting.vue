<template>
    <div class="w-full px-2  bg-zinc-700/40 rounded-lg">
        <button @click="isExpanded = !isExpanded"
            class="flex items-center justify-between w-full p-2 text-left text-primary font-medium rounded-lg">
            <div>
                <h2 class="text-lg font-bold text-zinc-200">{{ $t('walletSetting.title') }}</h2>
                <p class="text-muted-foreground">{{ $t('walletSetting.subtitle') }}</p>
            </div>
            <div class="mr-2">
                <Icon v-if="isExpanded" icon="lucide:chevrons-up" class="mr-2 h-4 w-4" />
                <Icon v-else icon="lucide:chevrons-down" class="mr-2 h-4 w-4" />
            </div>
        </button>
        <div v-if="isExpanded" class="space-y-6 px-2 py-2">
            <div class="space-y-4 mb-4">
                <Button as-child class="h-12 w-full">
                    <RouterLink to="/wallet/setting/phrase" class="w-full">
                        <Icon icon="lucide:eye-off" class="mr-2 h-4 w-4" /> {{ $t('walletSetting.showPhrase') }}
                    </RouterLink>
                </Button>
            </div>
        </div> 
    </div>
</template>

<script setup lang="ts">
import { Button } from '@/components/ui/button'
import { Icon } from '@iconify/vue'

const isExpanded = ref(false)

</script>