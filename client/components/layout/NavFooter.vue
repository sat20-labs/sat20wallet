<template>
  <nav class="border-t border-border h-16">
    <div class="flex justify-around items-center h-full">
      <Button
        v-for="item in navItems"
        as-child
        :key="item.to"
        size="icon"
        variant="ghost"
        @click="setActiveItem(item)"
      >
        <RouterLink :to="item.to">
          <Icon :icon="item.icon" class="text-lg" />
        </RouterLink>
      </Button>
    </div>
  </nav>
</template>
<script setup lang="ts">
import { ref } from 'vue'
import { Button } from '@/components/ui/button'

interface NavItem {
  icon: any
  label: string
  to: string
}

const navItems: NavItem[] = [
  { icon: 'solar:home-2-line-duotone', label: 'Home', to: '/wallet' },
  { icon: 'solar:settings-bold', label: 'Setting', to: '/wallet/setting' },
]

const activeItem = ref(navItems[0])

const setActiveItem = (item: NavItem) => {
  activeItem.value = item
}
</script>
