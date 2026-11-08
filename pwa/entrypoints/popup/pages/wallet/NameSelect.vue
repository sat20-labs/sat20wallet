<template>
  <LayoutSecond :title="$t('pwaUi.did.title')" :back="true">
    <main class="p-4 space-y-4">
      <p class="text-sm text-muted-foreground">{{ $t('pwaUi.did.description') }}</p>
      <p class="text-xs break-all">{{ $t('pwaUi.did.address') }}{{ address }}</p>
      <Label for="address-did">{{ $t('pwaUi.did.optional') }}</Label>
      <Input id="address-did" v-model="selectedName" :placeholder="$t('pwaUi.did.placeholder')" :disabled="busy" />
      <div v-if="nameList?.length" class="space-y-2">
        <p class="text-xs text-muted-foreground">{{ $t('pwaUi.did.ownedNames') }}</p>
        <Button v-for="name in nameList" :key="name.id" variant="outline" :disabled="busy" @click="selectedName = name.name">{{ name.name }}</Button>
      </div>
      <p v-if="error" role="alert" class="text-sm text-red-400">{{ error }}</p>
      <p v-if="saved" role="status" class="text-sm text-green-500">{{ $t('pwaUi.did.saved') }}</p>
      <div class="flex gap-2">
        <Button :disabled="busy" @click="saveName(selectedName)">{{ $t('pwaUi.did.save') }}</Button>
        <Button variant="outline" :disabled="busy || !currentName" @click="saveName('')">{{ $t('pwaUi.did.clear') }}</Button>
      </div>
    </main>
  </LayoutSecond>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useNameManager } from '@/composables/useNameManager'
import { useWalletStore } from '@/store'

const { t } = useI18n()

const walletStore = useWalletStore()
const { address, walletId, accountIndex } = storeToRefs(walletStore)
const { currentName, nameList, setCurrentName } = useNameManager()
const selectedName = ref(currentName.value)
const busy = ref(false)
const error = ref('')
const saved = ref(false)
watch([walletId, accountIndex, currentName], () => {
  selectedName.value = currentName.value
  saved.value = false
})
const saveName = async (did: string) => {
  busy.value = true
  error.value = ''
  saved.value = false
  try {
    await setCurrentName(address.value || '', did)
    selectedName.value = currentName.value
    saved.value = true
  } catch (e: any) { error.value = e?.message || t('pwaUi.did.failed') }
  finally { busy.value = false }
}
</script>
