<template>
  <div>
    <h2 class="text-2xl font-bold">Appearance</h2>
    <p class="text-muted-foreground">Customize your wallet appearance</p>
    <div class="grid gap-6 mt-4">
      <div class="flex items-center justify-between">
        <div class="space-y-0.5">
          <Label>Theme</Label>
          <div class="text-sm text-muted-foreground">
            Switch between light and dark mode
          </div>
        </div>
        <div class="flex space-x-2">
          <Button variant="outline" size="icon" @click="setTheme('light')">
            <Sun class="h-4 w-4" />
          </Button>
          <Button variant="outline" size="icon" @click="setTheme('dark')">
            <Moon class="h-4 w-4" />
          </Button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { Label } from '@/components/ui/label'
import { Button } from '@/components/ui/button'
// import { useTheme } from '@/components/theme-provider'
// import { Moon, Sun } from 'lucide-vue-next'

// const { setTheme } = useTheme()
const setTheme = (theme: string) => {
  console.log(theme)
}
</script>