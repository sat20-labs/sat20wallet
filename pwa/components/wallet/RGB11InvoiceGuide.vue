<template>
  <details class="rounded-lg border border-border px-3 py-2 text-sm">
    <summary class="cursor-pointer font-medium">{{ t('rgb11Invoice.guide.title') }}</summary>
    <ol class="mt-2 list-decimal space-y-2 pl-5 text-xs leading-relaxed text-muted-foreground">
      <li v-for="step in 6" :key="step">{{ t(`rgb11Invoice.guide.step${step}`) }}</li>
    </ol>
  </details>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
</script>
