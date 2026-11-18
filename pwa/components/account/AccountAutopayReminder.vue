<template>
  <div
    v-if="autopay && autopay.status !== 'ready'"
    class="mb-3 flex items-start justify-between gap-3 rounded-lg border border-amber-500/50 bg-amber-500/10 p-3"
  >
    <div class="flex min-w-0 items-start gap-2">
      <Icon icon="lucide:triangle-alert" class="mt-0.5 h-4 w-4 shrink-0 text-amber-400" />
      <div class="min-w-0">
        <div class="text-sm font-medium text-amber-400">
          {{ t('accountManagement.dkvsPaymentNeedsAttention') }}
        </div>
        <div class="mt-1 text-xs text-amber-400/90">
          {{ accountDisplayMessage(t, autopay.error_code) || t('accountManagement.ui.paymentUnavailable') }}
        </div>
      </div>
    </div>
    <Button v-if="autopay.can_fund" size="sm" variant="outline" class="shrink-0" @click="openFunding">
      {{ t('accountManagement.dkvsPay') }}
    </Button>
  </div>
  <div v-else-if="autopay?.expiry_height" class="mb-3 flex items-center justify-between gap-3 text-xs text-muted-foreground">
    <span>{{ t('accountManagement.dkvsExpiryEstimate', { height: autopay.expiry_height }) }}</span>
    <Button size="sm" variant="ghost" @click="openFunding">{{ t('accountManagement.dkvsPaymentDetails') }}</Button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Icon } from '@iconify/vue'
import { Button } from '@/components/ui/button'
import { accountDisplayMessage } from '@/utils/accountManagement'
import { useAccountManagementStore } from '@/store/accountManagement'
const router = useRouter()
const {t} = useI18n()
const accountState = useAccountManagementStore()
const autopay = computed(() => accountState.status?.autopay)
const openFunding = () => router.push('/wallet/setting/account-management')
</script>
