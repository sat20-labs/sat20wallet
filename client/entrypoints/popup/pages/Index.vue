<template>
  <div class="layout-container h-full flex flex-col items-center justify-center p-4">
    <img src="@/assets/sat20-logo.svg" alt="SAT20" class="w-16 h-16 mb-2" />
    <h1 class="text-3xl font-semibold mb-2 text-center">SAT20 Wallet</h1>
    <p class="text-muted-foreground/80 mb-4 text-center">
      Secure, simple, and powerful Bitcoin wallet
    </p>
    <div class="space-y-4 w-full p-2 max-w-md">
      <Button size="lg" as-child class="w-full">
        <RouterLink to="/create"> Create New Wallet </RouterLink>
      </Button>
      <Button size="lg" variant="secondary" class="w-full" as-child>
        <RouterLink to="/import"> Import Existing Wallet </RouterLink>
      </Button>
    </div>
    <p class="text-center text-sm text-muted-foreground/80 mt-6">
      By continuing, you agree to our Terms of Service and Privacy Policy
    </p>
  </div>
</template>

<script setup lang="ts">
import { Button } from '@/components/ui/button'
</script>
