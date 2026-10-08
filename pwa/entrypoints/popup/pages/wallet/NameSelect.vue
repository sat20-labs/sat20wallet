<template>
  <LayoutSecond title="设置地址 DID" :back="true">
    <main class="p-4 space-y-4">
      <p class="text-sm text-muted-foreground">DID 是当前钱包地址的账户属性，会随账户备份和恢复。</p>
      <p class="text-xs break-all">当前地址：{{ address }}</p>
      <Label for="address-did">地址 DID（可选）</Label>
      <Input id="address-did" v-model="selectedName" placeholder="例如 alice 或 alice.btc" :disabled="busy" />
      <div v-if="nameList?.length" class="space-y-2">
        <p class="text-xs text-muted-foreground">也可以选择该地址拥有的名字：</p>
        <Button v-for="name in nameList" :key="name.id" variant="outline" :disabled="busy" @click="selectedName = name.name">{{ name.name }}</Button>
      </div>
      <p v-if="error" role="alert" class="text-sm text-red-400">{{ error }}</p>
      <p v-if="saved" role="status" class="text-sm text-green-500">DID 已保存</p>
      <div class="flex gap-2">
        <Button :disabled="busy" @click="saveName(selectedName)">保存 DID</Button>
        <Button variant="outline" :disabled="busy || !currentName" @click="saveName('')">清空 DID</Button>
      </div>
    </main>
  </LayoutSecond>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useNameManager } from '@/composables/useNameManager'
import { useWalletStore } from '@/store'

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
  } catch (e: any) { error.value = e?.message || '保存 DID 失败' }
  finally { busy.value = false }
}
</script>
