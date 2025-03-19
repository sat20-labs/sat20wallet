<template>
  <CopyCard :text="address" class="w-full">
    <Button asChild variant="link">
      <a
        :href="`https://mempool.space/zh/testnet4/address/${address}`"
        target="_blank"
      >
        {{ hideAddress(address) }}
      </a>
    </Button>
  </CopyCard>
  <!-- <BalanceDisplay :balance="balance" /> -->
  <L1AssetsTabs />
  <div class="flex justify-center items-center pl-4 sm:pl-10 gap-2">
    <Button :disabled="loading" variant="secondary" size="icon" @click="refreshL1Assets">
      <Icon
        icon="solar:refresh-broken"
        :class="{ 'animate-spin': loading }"
      />
    </Button>
  </div>
</template>

<script setup lang="ts">
import { Button } from '@/components/ui/button'
import L1AssetsTabs from '@/components/asset/L1AssetsTabs.vue'
import CopyCard from '@/components/common/CopyCard.vue'
import { hideAddress } from '~/utils'
import { useL1Assets } from '@/composables'
import { useChannelStore, useWalletStore, useL1Store } from '@/store'

const { refreshL1Assets, loading } = useL1Assets()
const walletStore = useWalletStore()

const { address } = storeToRefs(walletStore)

</script>
