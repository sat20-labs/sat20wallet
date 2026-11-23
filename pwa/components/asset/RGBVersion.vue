<template>
  <p v-if="version" class="text-xs text-muted-foreground">
    {{ $t('rgb11Transfer.protocolVersion', { version }) }}
  </p>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import walletManager from '@/utils/sat20'

const version = ref('')

onMounted(async () => {
  const [error, info] = await walletManager.getVersion()
  if (!error && info) version.value = info.rgb_protocol_version
})
</script>
