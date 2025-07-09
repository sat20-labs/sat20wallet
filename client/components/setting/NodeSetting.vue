<template>
  <div class="w-full px-2  bg-zinc-700/40 rounded-lg">
    <button @click="isExpanded = !isExpanded"
      class="flex items-center justify-between w-full p-2 text-left text-primary font-medium rounded-lg">
      <div>
        <h2 class="text-lg font-bold text-zinc-200">Node Setting</h2>
        <p class="text-muted-foreground">{{ $t('securitySetting.subtitle') }}</p>
      </div>
      <div class="mr-2">
        <Icon v-if="isExpanded" icon="lucide:chevrons-up" class="mr-2 h-4 w-4" />
        <Icon v-else icon="lucide:chevrons-down" class="mr-2 h-4 w-4" />
      </div>
    </button>
    <div v-if="isExpanded" class="space-y-6 px-2 py-4">

      <Button as-child class="h-12 w-full">
        <RouterLink to="/wallet/setting/node" class="w-full">
          加入节点
        </RouterLink>
      </Button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

const isExpanded = ref(false)
const autoLockTime = ref('5')
const hideBalance = ref(false)
</script>