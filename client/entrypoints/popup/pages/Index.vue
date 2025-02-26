<template>
  <div class="h-full flex flex-col items-center justify-center p-4">
    <h1 class="text-3xl font-bold mb-2 text-center">Welcome to BTC Wallet</h1>
    <p class="text-muted-foreground mb-4 text-center">
      Secure, simple, and powerful Bitcoin wallet
    </p>
    <div class="space-y-4 w-full max-w-md">
      <Button size="lg" as-child class="w-full">
        <RouterLink to="/create">
          <Wallet class="mr-2 h-5 w-5" />
          Create New Wallet
        </RouterLink>
      </Button>
      <Button variant="outline" class="w-full" @click="toImport">
        <Download class="mr-2 h-5 w-5" />
        Import Existing Wallet
      </Button>
    </div>
    <p class="text-center text-sm text-muted-foreground mt-6">
      By continuing, you agree to our Terms of Service and Privacy Policy
    </p>
  </div>
</template>

<script setup lang="ts">
import { Button } from '@/components/ui/button'
import { useRouter } from 'vue-router'
import walletManager from '@/utils/sat20'
const { approveData, approve } = useApprove()
const router = useRouter()
const toImport = () => {
  router.push('/import')
}
</script>
