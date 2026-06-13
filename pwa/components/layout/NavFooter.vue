<template>
  <nav class="border-t border-border h-16">
    <div class="flex justify-around items-center h-full">
      <button
        v-for="item in navItems"
        :key="item.to"
        type="button"
        class="inline-flex h-10 w-10 items-center justify-center rounded-md text-sm font-medium transition-colors hover:bg-accent hover:text-accent-foreground"
        @click.prevent.stop="setActiveItem(item)"
      >
        <Icon :icon="item.icon" class="text-lg text-muted-foreground" />
      </button>
    </div>
  </nav>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import { useRouter } from 'vue-router';
import { Icon } from '@iconify/vue';
import { openLink } from '@/utils/browser';

interface NavItem {
  icon: any;
  label: string;
  to?: string; // to 属性变为可选
  external?: boolean; // 是否是外部链接
  action?: () => void; // 自定义操作
}

const router = useRouter();

const navItems: NavItem[] = [
  { icon: 'lucide:house', label: 'Home', to: '/wallet' },
  {
    icon: 'lucide:globe',
    label: 'DApp',
    action: () => {
      router.push('/wallet/dapp');
    }
  },
  { icon: 'lucide:wrench', label: 'Tools', to: '/wallet/tools' },
  { icon: 'lucide:settings', label: 'Setting', to: '/wallet/setting' },
];

const activeItem = ref(navItems[0]);

const setActiveItem = (item: NavItem) => {
  activeItem.value = item;
  // 如果有自定义操作，执行操作而不是导航
  if (item.action) {
    item.action();
  } else if (item.external && item.to) {
    openWindow(item.to);
  } else if (item.to) {
    router.push(item.to);
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
