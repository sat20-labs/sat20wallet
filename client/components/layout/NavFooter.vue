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
        <template v-if="item.external">
          <!-- 外部链接嵌入 -->
          <a @click.prevent="openWindow(item.to)">
            <Icon :icon="item.icon" class="text-lg text-muted-foreground" />
          </a>
        </template>
        <template v-else>
          <!-- 内部路由导航 -->
          <RouterLink :to="item.to">
            <Icon :icon="item.icon" class="text-lg text-muted-foreground" />
          </RouterLink>
        </template>
      </Button>
    </div>
  </nav>

  <!-- iframe 容器 -->
  <div v-if="iframeSrc" class="iframe-container">
    <iframe
      :src="iframeSrc"
      class="w-full h-full border-none"
      frameborder="0"
    ></iframe>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import { Button } from '@/components/ui/button';

interface NavItem {
  icon: any;
  label: string;
  to: string;
  external?: boolean; // 是否是外部链接
}

const navItems: NavItem[] = [
  { icon: 'lucide:house', label: 'Home', to: '/wallet' },
  { icon: 'lucide:arrow-right-left', label: 'Trade', to: 'https://satsnet.test.ordx.market/market', external: true },
  { icon: 'lucide:settings', label: 'Setting', to: '/wallet/setting' },
];

const activeItem = ref(navItems[0]);
const iframeSrc = ref<string | null>(null); // iframe 的 src

const setActiveItem = (item: NavItem) => {
  activeItem.value = item;
};

const openWindow = (url: string) => {
  window.open(url, '_blank'); // 在新标签页中打开链接
};
</script>

