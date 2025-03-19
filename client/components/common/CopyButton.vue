<template>
  <Button variant="ghost" size="icon" @click="copyHandler">
    <Icon v-if="!copied" icon="solar:copy-linear"/>
    <Icon v-else icon="solar:check-read-linear"  />
  </Button>
</template>

<script setup lang="ts">
import { Button } from '@/components/ui/button';
import { useClipboard } from '@vueuse/core';
import { Icon, } from '@iconify/vue';

interface Props {
  text?: string | null;
}

const props = defineProps<Props>();

const { copy, copied, isSupported } = useClipboard();

const copyHandler = () => {
  if (isSupported && props.text) {
    copy(props.text);
  }
};
</script>
