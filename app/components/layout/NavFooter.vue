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
        <template v-if="item.action">
          <!-- 自定义操作 -->
          <button @click="setActiveItem(item)" class="p-0 border-none bg-transparent">
            <Icon :icon="item.icon" class="text-lg text-muted-foreground" />
          </button>
        </template>
        <template v-else-if="item.external && item.to">
          <!-- 外部链接嵌入 -->
          <a @click.prevent="openWindow(item.to)">
            <Icon :icon="item.icon" class="text-lg text-muted-foreground" />
          </a>
        </template>
        <template v-else-if="item.to">
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
import { Icon } from '@iconify/vue';
import { useWebViewBridge } from '@/composables/useWebViewBridge';
import { openLink } from '@/utils/browser';

interface NavItem {
  icon: any;
  label: string;
  to?: string; // to 属性变为可选
  external?: boolean; // 是否是外部链接
  action?: () => void; // 自定义操作
}

const { openDApp } = useWebViewBridge();

// 默认DApp URL
const DEFAULT_DAPP_URL = 'https://satsnet.ordx.market';

const navItems: NavItem[] = [
  { icon: 'lucide:house', label: 'Home', to: '/wallet' },
  {
    icon: 'lucide:globe',
    label: 'DApp',
    action: () => {
      // 直接打开DApp WebView
      openDApp(DEFAULT_DAPP_URL);
    }
  },
  { icon: 'lucide:settings', label: 'Setting', to: '/wallet/setting' },
];

const activeItem = ref(navItems[0]);
const iframeSrc = ref<string | null>(null); // iframe 的 src

const setActiveItem = (item: NavItem) => {
  activeItem.value = item;
  // 如果有自定义操作，执行操作而不是导航
  if (item.action) {
    item.action();
  }
};

const openWindow = async (url: string) => {
  try {
    await openLink(url); // 使用统一的链接打开函数
  } catch (error) {
    console.error('打开链接失败:', error)
  }
};
</script>

