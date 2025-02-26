<template>
  <LayoutSecond title="Receive">
    <div class="flex flex-col gap-6 items-center">
      <QrcodeVue :value="address" :size="200" class="mx-auto" v-if="address" />
      <div class="w-60">
        <CopyCard :text="address" v-if="address"
          ><div>{{ hideAddress(address) }}</div></CopyCard
        >
      </div>
    </div>
  </LayoutSecond>
</template>

<script setup lang="ts">
import QrcodeVue from 'qrcode.vue'
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import CopyCard from '@/components/common/CopyCard.vue'
import { hideAddress } from '@/utils'
import { useWalletStore } from '@/store'

const walletStore = useWalletStore()
const { address } = storeToRefs(walletStore)
</script>

