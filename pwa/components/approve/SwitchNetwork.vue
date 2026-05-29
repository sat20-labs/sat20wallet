<template>
  <LayoutApprove @confirm="confirm" @cancel="cancel">
    <div class="p-6 space-y-6">
      <h2 class="text-2xl font-semibold text-center">
        {{ $t('switchNetwork.title') }}
      </h2>

      <div class="flex items-center justify-center gap-4 sm:gap-6">
        <div
          class="px-6 py-3 bg-zinc-800 hover:bg-zinc-700 rounded-lg font-medium text-white transition-colors cursor-pointer"
        >
          {{ network }}
        </div>

        <div class="text-gray-400">></div>

        <div
          class="px-6 py-3 bg-zinc-800 hover:bg-zinc-700 rounded-lg font-medium text-white transition-colors cursor-pointer"
        >
          {{ props.data.network }}
        </div>
      </div>
    </div>
  </LayoutApprove>
</template>

<script setup lang="ts">
import LayoutApprove from '@/components/layout/LayoutApprove.vue'
import { useWalletStore } from '@/store'
import { storeToRefs } from 'pinia'
interface Props {
  data: any
}

const props = defineProps<Props>()

const walletStore = useWalletStore()
const { network } = storeToRefs(walletStore)
const emit = defineEmits(['confirm', 'cancel'])

const confirm = async () => {
  setTimeout(() => {
    emit('confirm', props.data.network)
  }, 500);
  await walletStore.setNetwork(props.data.network)
}
const cancel = () => {
  emit('cancel')
}
</script>

<style lang="less" scoped></style>
