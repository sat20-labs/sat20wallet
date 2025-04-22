<template>
  <div class="w-full px-2  bg-zinc-700/40 rounded-lg">
    <button @click="isExpanded = !isExpanded"
      class="flex items-center justify-between w-full p-2 text-left text-primary font-medium rounded-lg">
      <div>
        <h2 class="text-lg font-bold text-zinc-200">Other Options</h2>
        <p class="text-muted-foreground">Other preferences</p>
      </div>
      <div class="mr-2">
          <Icon v-if="isExpanded" icon="lucide:chevrons-up" class="mr-2 h-4 w-4" />
          <Icon v-else icon="lucide:chevrons-down" class="mr-2 h-4 w-4" />
      </div>
    </button>
    <div v-if="isExpanded" class="space-y-6 px-2 py-4">
      <div class="flex items-center justify-between border-t border-zinc-900/30 pt-4">        
        <div class="text-sm text-muted-foreground">
          Environment Switch:
        </div>        
        <Select v-model="computedEnv">
          <SelectTrigger class="w-[180px] bg-gray-900/30 mb-4">
            <SelectValue placeholder="Select environment" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="dev">Development</SelectItem>
            <SelectItem value="test">Test</SelectItem>
            <SelectItem value="prod">Production</SelectItem>
          </SelectContent>
        </Select>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { useGlobalStore, type Env } from '@/store/global'

const isExpanded = ref(false)
const globalStore = useGlobalStore()

const computedEnv = computed<Env>({
  get: () => globalStore.env,
  set: (newValue) => {
    globalStore.setEnv(newValue)
    window.location.reload()
  }
})
</script>