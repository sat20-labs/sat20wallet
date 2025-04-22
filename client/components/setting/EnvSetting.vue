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
import { browser } from 'wxt/browser'
import { Message } from '@/types/message'

const globalStore = useGlobalStore()

const computedEnv = computed<Env>({
  get: () => globalStore.env,
  set: async (newValue) => {
    await globalStore.setEnv(newValue)

    try {
      console.log(`Sending ENV_CHANGED message with payload: ${newValue}`)
      await browser.runtime.sendMessage({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.ENV_CHANGED,
        data: { env: newValue },
        metadata: { from: 'SETTINGS_PAGE' }
      })
    } catch (error) {
      console.error('Failed to send ENV_CHANGED message to background:', error)
    }

    window.location.reload()
  }
})
</script>