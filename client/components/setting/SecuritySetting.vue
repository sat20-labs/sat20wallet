<template>
  <div class="w-full px-2  bg-zinc-700/40 rounded-lg">
    <button @click="isExpanded = !isExpanded"
      class="flex items-center justify-between w-full p-2 text-left text-primary font-medium rounded-lg">
      <div>
        <h2 class="text-lg font-bold text-zinc-200">Security Options</h2>
        <p class="text-muted-foreground">Manage your wallet security preferences</p>
      </div>
      <div class="mr-2">
        <Icon v-if="isExpanded" icon="lucide:chevrons-up" class="mr-2 h-4 w-4" />
        <Icon v-else icon="lucide:chevrons-down" class="mr-2 h-4 w-4" />
      </div>
    </button>
    <div v-if="isExpanded" class="space-y-6 px-2 py-4">
      <div class="flex items-center justify-between border-t border-zinc-900/30 pt-4">
        <div class="space-y-0.5">
          <Label>Auto-lock Timer</Label>
          <div class="text-sm text-muted-foreground">
            Automatically lock wallet after inactivity
          </div>
        </div>
        <Select v-model="autoLockTime" default-value="5">
          <SelectTrigger class="w-[180px] bg-gray-900/30">
            <SelectValue placeholder="Select time" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="1">1 minute</SelectItem>
            <SelectItem value="5">5 minutes</SelectItem>
            <SelectItem value="15">15 minutes</SelectItem>
            <SelectItem value="30">30 minutes</SelectItem>
          </SelectContent>
        </Select>
      </div>

      <div class="flex items-center justify-between border-t border-zinc-900/30 pt-4">
        <div class="space-y-0.5 mb-4">
          <Label>Hide Balance</Label>
          <div class="text-sm text-muted-foreground">
            Automatically blur balance on launch
          </div>
        </div>
        <Switch v-model="hideBalance" />
      </div>
      <Button as-child class="h-12 w-full">
        <RouterLink to="/wallet/setting/phrase" class="w-full">
          <Icon icon="lucide:eye-off" class="mr-2 h-4 w-4" /> Show Phrase
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