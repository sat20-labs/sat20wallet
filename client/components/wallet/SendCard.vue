<template>
  <div class="max-w-md w-full mx-auto space-y-6">
    <h1 class="text-2xl font-bold text-center text-foreground">Send BTC</h1>

    <div class="space-y-4">
      <div class="space-y-2">
        <Label for="address">Receiving Address</Label>
        <Input
          id="address"
          v-model="modelAddress"
          placeholder="Enter BTC address"
        />
      </div>

      <div class="space-y-2">
        <Label for="amount">Amount (BTC)</Label>
        <Input
          id="amount"
          v-model="modelAmount"
          type="number"
          step="0.00000001"
          min="0"
          placeholder="0.00000000"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { defineModel } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

const modelAddress = defineModel<string>('address', {
  default: '',
})
const modelAmount = defineModel<number>('amount', {
  default: undefined,
})
</script>
