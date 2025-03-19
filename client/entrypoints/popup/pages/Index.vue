<template>
  <div class="h-full flex flex-col items-center justify-center p-4">
    <h1 class="text-3xl font-bold mb-2 text-center">Welcome to BTC Wallet</h1>
    <p class="text-muted-foreground mb-4 text-center">
      Secure, simple, and powerful Bitcoin wallet
    </p>
    <div class="space-y-4 w-full max-w-md">
      <Button size="lg" as-child class="w-full">
        <RouterLink to="/create"> Create New Wallet </RouterLink>
      </Button>
      <Button variant="outline" class="w-full" as-child>
        <RouterLink to="/import"> Import Existing Wallet </RouterLink>
      </Button>
    </div>
    <p class="text-center text-sm text-muted-foreground mt-6">
      By continuing, you agree to our Terms of Service and Privacy Policy
    </p>
  </div>
</template>

<script setup lang="ts">
import { Button } from '@/components/ui/button'
</script>
