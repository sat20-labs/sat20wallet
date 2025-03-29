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
          <Icon :icon="item.icon" class="text-lg text-muted-foreground" />
        </RouterLink>
      </Button>
    </div>
  </nav>
</template>
<script setup lang="ts">
import { Button } from '@/components/ui/button'

interface NavItem {
  icon: any
  label: string
  to: string
}

const navItems: NavItem[] = [
  { icon: 'lucide:house', label: 'Home', to: '/wallet' },
  { icon: 'lucide:arrow-right-left', label: 'Trade', to: '/trade' },
  { icon: 'lucide:settings', label: 'Setting', to: '/wallet/setting' },
]

const activeItem = ref(navItems[0])

const setActiveItem = (item: NavItem) => {
  activeItem.value = item
}
</script>
