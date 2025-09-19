<template>
  <div class="layout-container h-full flex flex-col items-center justify-center p-4">
    <img src="@/assets/sat20-logo.svg" alt="ORDX" class="w-16 h-16 mb-2" />
    <h1 class="text-3xl font-semibold mb-2 text-center">{{ $t('index.title') }}</h1>
    <p class="text-muted-foreground/80 mb-4 text-center">
      {{ $t('index.subtitle') }}
    </p>
    <div class="space-y-4 w-full p-2 max-w-md">
      <Button size="lg" as-child class="w-full">
        <RouterLink to="/create"> {{ $t('index.createWallet') }} </RouterLink>
      </Button>
      <Button size="lg" variant="secondary" class="w-full" as-child>
        <RouterLink to="/import"> {{ $t('index.importWallet') }} </RouterLink>
      </Button>
    </div>
    <p class="text-center text-sm text-muted-foreground/80 mt-6">
      {{ $t('index.agreement') }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { Button } from '@/components/ui/button'
</script>
