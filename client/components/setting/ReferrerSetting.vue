<template>
  <div class="w-full px-2 bg-zinc-700/40 rounded-lg">
    <button @click="isExpanded = !isExpanded"
      class="flex items-center justify-between w-full p-2 text-left text-primary font-medium rounded-lg">
      <div>
        <h2 class="text-lg font-bold text-zinc-200">推荐人管理</h2>
        <p class="text-muted-foreground">管理推荐人注册和绑定</p>
      </div>
      <div class="mr-2">
        <Icon v-if="isExpanded" icon="lucide:chevrons-up" class="mr-2 h-4 w-4" />
        <Icon v-else icon="lucide:chevrons-down" class="mr-2 h-4 w-4" />
      </div>
    </button>
    <div v-if="isExpanded" class="space-y-6 px-2 py-4">
      <Button as-child class="h-12 w-full mb-2">
        <RouterLink to="/wallet/setting/referrer/register" class="w-full">
          注册为推荐人
        </RouterLink>
      </Button>
      <Button as-child variant="outline" class="h-12 w-full">
        <RouterLink to="/wallet/setting/referrer/bind" class="w-full">
          绑定推荐人
        </RouterLink>
      </Button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { Button } from '@/components/ui/button'

const isExpanded = ref(false)
</script> 