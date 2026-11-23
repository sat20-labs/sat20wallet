<template>
  <nav class="shrink-0 border-t border-border bg-background pb-[env(safe-area-inset-bottom)]">
    <div class="mx-auto grid min-h-16 max-w-3xl grid-cols-4 gap-1 px-2 py-1">
      <button
        v-for="item in navItems"
        :key="item.to"
        type="button"
        :aria-label="$t(item.label)"
        :aria-current="isActive(item.to) ? 'page' : undefined"
        class="flex min-h-11 min-w-0 flex-col items-center justify-center gap-1 rounded-lg px-1 py-1 text-center text-[11px] font-medium leading-tight transition-colors hover:bg-accent"
        :class="isActive(item.to) ? 'bg-accent text-foreground' : 'text-muted-foreground'"
        @click.prevent.stop="router.push(item.to)"
      >
        <component :is="item.icon" aria-hidden="true" class="size-4 shrink-0" />
        <span class="break-words">{{ $t(item.label) }}</span>
      </button>
    </div>
  </nav>
</template>

<script setup lang="ts">
import { useRoute, useRouter } from 'vue-router'
import { Globe, House, Settings, Wrench } from 'lucide-vue-next'

const router = useRouter()
const route = useRoute()

const navItems = [
  { icon: House, label: 'navigation.home', to: '/wallet' },
  { icon: Globe, label: 'navigation.dapp', to: '/wallet/dapp' },
  { icon: Wrench, label: 'tools.title', to: '/wallet/tools' },
  { icon: Settings, label: 'navigation.setting', to: '/wallet/setting' },
]

const isActive = (path: string) => route.path === path ||
  (path === '/wallet/setting' && route.path.startsWith(`${path}/`))
</script>
