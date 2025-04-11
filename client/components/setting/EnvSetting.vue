<template>
  <div>
    <div class="space-y-6">
      <div class="flex items-center justify-between">
        <div class="space-y-0.5">
          <Label>Environment</Label>
          <div class="text-sm text-muted-foreground">
            Switch between dev, test, and prod environments
          </div>
        </div>
        <Select v-model="computedEnv">
          <SelectTrigger class="w-[180px]">
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

const globalStore = useGlobalStore()

const computedEnv = computed<Env>({
  get: () => globalStore.env,
  set: (newValue) => {
    globalStore.setEnv(newValue)
    window.location.reload()
  }
})
</script>