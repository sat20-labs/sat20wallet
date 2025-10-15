<template>
  <Card class="mb-6 cursor-pointer hover:bg-accent/50 transition-colors">
    <CardContent class="flex items-center justify-between p-4">
      <div class="flex items-center gap-3">
        <User2 class="w-6 h-6 text-gray-400" />
        <div>
          <h2 class="text-lg font-medium">{{ accountName }}</h2>
          <p class="text-gray-400 text-sm flex items-center">
            {{ truncatedAddress }}
            <CopyButton :text="props.address as any" />
          </p>
        </div>
      </div>
      <ChevronRight class="w-5 h-5 text-gray-400" />
    </CardContent>
  </Card>
</template>

<script setup lang="ts">
import { Card, CardContent } from '@/components/ui/card'
import { computed } from 'vue'
import CopyButton from '@/components/common/CopyButton.vue'
import { hideAddress } from '@/utils'
interface Props {
  accountName?: string | null
  address: string | null
}
const props = withDefaults(defineProps<Props>(), {
  accountName: '',
  address: '',
})

const truncatedAddress = computed(() => {
  return hideAddress(props.address)
})

const copyAddress = (e: any) => {
  e.stopPropagation()
  if (!props.address) return
  navigator.clipboard.writeText(props.address)
  // 可以添加复制成功的提示
}
</script>
