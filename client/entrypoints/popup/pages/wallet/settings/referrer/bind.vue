<template>
  <LayoutSecond :title="$t('referrerManagement.bindReferrer')">
    <div class="max-w-xl mx-auto my-4">
      <div class="mb-6">
        <p class="flex justify-start items-center text-sm text-muted-foreground"><Icon icon="lucide:badge-info" class="w-10 h-10 mr-2 text-green-600"/>
          {{ $t('referrerManagement.bindReferrerDescription') }}</p>
      </div>
      <div class="flex flex-col gap-4">
        <div class="grid w-full items-center gap-1.5">
          <Label for="referrerName">{{ $t('referrerManagement.referrerName') }}</Label>
          <Input id="referrerName" v-model="referrerName" :placeholder="$t('referrerManagement.referrerNamePlaceholder')" />
        </div>
        <div class="grid w-full items-center gap-1.5">
          <Label for="serverPubKey">{{ $t('referrerManagement.serverPubKey') }}</Label>
          <Input id="serverPubKey" v-model="serverPubKey" :placeholder="$t('referrerManagement.serverPubKeyPlaceholder')" />
        </div>
        <Button aria-label="Bind Referrer" @click="onBind" :loading="isLoading">
          {{ $t('referrerManagement.bindReferrer') }}
        </Button>
      </div>
      <div v-if="resultMsg" class="mt-4">
        <Alert :variant="resultSuccess ? 'default' : 'destructive'">
          <AlertTitle>{{ resultSuccess ? $t('referrerManagement.BindSuccess') :
            $t('referrerManagement.BindFailure') }}</AlertTitle>
          <AlertDescription>{{ resultMsg }}</AlertDescription>
        </Alert>
      </div>
      <Dialog v-model:open="showConfirm">
        <DialogContent>
          <DialogHeader>
            <DialogTitle><span class="flex justify-start items-center text-zinc-300 text-lg"><Icon icon="lucide:message-circle-question-mark" class="w-12 h-12 mr-1 text-red-600"/>{{ $t('referrerManagement.confirmBindDescription') }}</span><br></DialogTitle>
            <hr class="my-2 border-zinc-950" />           
            <DialogDescription class="text-zinc-300">              
              <p class="py-1 mt-4">
                <span class="text-zinc-500 mr-4">{{ $t('referrerManagement.referrerName') }} :</span>  <span
                  class="text-zinc-300">{{ referrerName }}</span>
              </p>
              <p class="py-1">
                <span class="text-zinc-500 mr-4">{{ $t('referrerManagement.serverPubKey') }} :</span> <span
                  class="text-zinc-300 mr-2">{{ serverPubKey }} </span> 
              </p>
             
            </DialogDescription>
          </DialogHeader>
         
          <DialogFooter>           
            <div class="flex justify-end gap-3">
              <Button @click="confirmBind" :loading="isLoading" class="w-36">{{ $t('referrerManagement.confirm') }}</Button>
              <Button variant="secondary" @click="showConfirm = false" class="w-36">{{ $t('referrerManagement.cancel') }}</Button>                
            </div>   
          </DialogFooter>

        </DialogContent>
      </Dialog>
    </div>
  </LayoutSecond>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { storeToRefs } from 'pinia'
import stp from '@/utils/stp'
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert, AlertTitle, AlertDescription } from '@/components/ui/alert'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { useGlobalStore } from '@/store/global'
import { useWalletStore } from '@/store/wallet'
import { getConfig } from '@/config/wasm'


const isLoading = ref(false)
const showConfirm = ref(false)
const resultMsg = ref('')
const resultSuccess = ref(false)
const referrerName = ref('')
const serverPubKey = ref('')
const globalStore = useGlobalStore()
const walletStore = useWalletStore()
const { env } = storeToRefs(globalStore)
const { network } = storeToRefs(walletStore)

// 获取配置中的第一个Peer的公钥
const config = getConfig(env.value, network.value)
const firstPeer = config.Peers[1]
if (firstPeer) {
  // Peer格式为 "b@<pubkey>@<url>"，我们需要提取pubkey部分
  const parts = firstPeer.split('@')
  if (parts.length >= 2) {
    serverPubKey.value = parts[1]
  }
}

function onBind() {
  if (!referrerName.value || !serverPubKey.value) {
    resultMsg.value = '请填写完整信息'
    resultSuccess.value = false
    return
  }
  showConfirm.value = true
}

async function confirmBind() {
  isLoading.value = true
  resultMsg.value = ''
  showConfirm.value = false
  try {
    const [err, res] = await stp.bindReferrerForServer(referrerName.value, serverPubKey.value)
    if (err) {
      resultMsg.value = err.message || '绑定失败'
      resultSuccess.value = false
    } else {
      resultMsg.value = '绑定成功！'
      resultSuccess.value = true
      // 清空输入
      referrerName.value = ''
      serverPubKey.value = ''
    }
  } catch (e: any) {
    resultMsg.value = e.message || '未知错误'
    resultSuccess.value = false
  } finally {
    isLoading.value = false
  }
}
</script> 