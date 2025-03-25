<template>
  <div>
    <h2 class="text-2xl font-bold text-primary/60">Security Settings</h2>    
    <p class="text-muted-foreground">Manage your wallet security preferences</p>
    <hr class="mb-4 mt-2 border-t-1 border-accent">
    <div class="space-y-6 mt-4">
      <div class="flex items-center justify-between">
        <div class="space-y-0.5">
          <Label>Auto-lock Timer</Label>
          <div class="text-sm text-muted-foreground">
            Automatically lock wallet after inactivity
          </div>
        </div>
        <Select v-model="autoLockTime" default-value="5">
          <SelectTrigger class="w-[180px]">
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
      <hr class="mb-2 mt-2 border-t-1 border-accent">
      <div class="flex items-center justify-between">
        <div class="space-y-0.5">
          <Label>Hide Balance</Label>
          <div class="text-sm text-muted-foreground">
            Automatically blur balance on launch
          </div>
        </div>
        <Switch v-model="hideBalance" />
      </div>
      
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

const autoLockTime = ref('5')
const hideBalance = ref(false)
</script>