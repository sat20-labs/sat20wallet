<template>
  <AlertDialog v-model:open="isOpen">
    <AlertDialogContent class="w-[350px] rounded-lg bg-zinc-900">
      <AlertDialogHeader>
        <AlertDialogTitle>{{ $t('assetOperationDialog.lockExpandTitle') }}</AlertDialogTitle>
        <AlertDialogDescription>
          {{ $t('assetOperationDialog.lockExpandDescription') }}
        </AlertDialogDescription>
      </AlertDialogHeader>

      <div class="space-y-2 rounded-lg border border-zinc-700 bg-zinc-800 p-3 text-sm">
        <div class="flex justify-between gap-4">
          <span class="text-zinc-400">{{ $t('assetOperationDialog.lockExpandAsset') }}</span>
          <span class="break-all text-right text-zinc-200">{{ assetTicker || assetKey }}</span>
        </div>
        <div class="flex justify-between gap-4">
          <span class="text-zinc-400">{{ $t('assetOperationDialog.lockExpandRequested') }}</span>
          <span class="text-zinc-200">{{ requestedAmount }}</span>
        </div>
        <div class="flex justify-between gap-4">
          <span class="text-zinc-400">{{ $t('assetOperationDialog.lockExpandAvailable') }}</span>
          <span class="text-zinc-200">{{ availableAmount }}</span>
        </div>
        <div class="flex justify-between gap-4">
          <span class="text-zinc-400">{{ $t('assetOperationDialog.lockExpandFeeRate') }}</span>
          <span class="text-zinc-200">{{ btcFeeRate }} sats/vB</span>
        </div>
      </div>

      <div class="rounded-lg border border-yellow-700 bg-yellow-900/20 p-3 text-sm text-yellow-300">
        {{ $t('assetOperationDialog.lockExpandFeeInfo') }}
      </div>

      <AlertDialogFooter>
        <AlertDialogCancel :disabled="busy">
          {{ $t('assetOperationDialog.cancel') }}
        </AlertDialogCancel>
        <AlertDialogAction :disabled="busy" @click.prevent="$emit('confirm')">
          {{ busy ? $t('assetOperationDialog.lockExpandProcessing') : $t('assetOperationDialog.lockExpandConfirm') }}
        </AlertDialogAction>
      </AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>
</template>

<script setup lang="ts">
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'

defineProps<{
  assetKey: string
  assetTicker?: string
  requestedAmount: string
  availableAmount: string
  btcFeeRate: string | number
  busy?: boolean
}>()

defineEmits<{ (event: 'confirm'): void }>()

const isOpen = defineModel('open', { type: Boolean, default: false })
</script>
