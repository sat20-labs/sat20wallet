<template>
  <CopyCard :text="address" class="w-full">
    <Button asChild variant="link">
      <a
        :href="`https://satstestnet-mempool.sat20.org/address/${address}`"
        target="_blank"
      >
        {{ hideAddress(address) }}
      </a>
    </Button>
  </CopyCard>
  <L2AssetsTabs />
  <div class="flex justify-center items-center pl-4 sm:pl-10 gap-2">
    <!-- <Button
          :disabled="getLoading"
          variant="default"
          @click="l2Store.retry()"
        >
          <Icon
            icon="lucide:refresh-cw"
            class="h-4 w-4"
            :class="{ 'animate-spin': getLoading }"
          />
        </Button> -->
  </div>
</template>

<script lang="ts" setup>
import CopyCard from '@/components/common/CopyCard.vue'
import { Button } from '@/components/ui/button'
import { hideAddress } from '~/utils'
import { useWalletStore } from '@/store'
import L2AssetsTabs from '@/components/asset/L2AssetsTabs.vue'

const walletStore = useWalletStore()
const { address } = storeToRefs(walletStore)
</script>
