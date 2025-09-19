<template>
  <LayoutSecond :title="$t('referrerManagement.registerAsReferrer')" class="max-w-2xl mx-auto">
    <div class="max-w-xl mx-auto my-8">
      <div class="flex flex-col gap-4">
        <div class="mb-4">
          <p class="flex justify-start items-center text-sm text-muted-foreground">
            <Icon icon="lucide:badge-info" class="w-12 h-12 mr-2 text-green-600" />
            {{ $t('referrerManagement.referrerRegistrationDescription') }}
          </p>
        </div>
        <div class="grid w-full items-center gap-1.5">
          <Label for="name">{{ $t('referrerManagement.referrerName') }}</Label>
          <Select v-model="name" :disabled="isLoadingNames">
            <SelectTrigger>
              <SelectValue :placeholder="$t('referrerManagement.referrerNamePlaceholder')" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem v-if="nameList && nameList.length > 0" v-for="nameItem in nameList" :key="nameItem.id" :value="nameItem.name">
                {{ nameItem.name }}
              </SelectItem>
              <SelectItem v-else value="no-names" disabled>
                {{ isLoadingNames ? '加载中...' : '暂无可用名字' }}
              </SelectItem>
            </SelectContent>
          </Select>
        </div>
        <!-- 当nameList为空且不在加载状态时显示提示 -->
        <Alert v-if="!isLoadingNames && (!nameList || nameList.length === 0)" variant="default">
          <AlertTitle>提示</AlertTitle>
          <AlertDescription>
            当前地址没有可用的名字，请先注册名字后再进行推荐人注册。
          </AlertDescription>
        </Alert>
        <div class="grid w-full items-center gap-1.5">
          <Label for="feeRate">{{ $t('referrerManagement.gasFeeRate') }}</Label>
          <Input id="feeRate" v-model="btcFeeRate" type="number" min="0" max="100"
            :placeholder="$t('referrerManagement.gasFeeRatePlaceHolder')" />
        </div>
        <Button aria-label="{{$t('referrerManagement.registerAsReferrer')}}" @click="onRegister" :loading="isLoading">
          {{ $t('referrerManagement.registerAsReferrer') }}
        </Button>
        <Alert v-if="resultMsg" :variant="resultSuccess ? 'default' : 'destructive'">
          <AlertTitle>{{ resultSuccess ? $t('referrerManagement.RegistrationSuccess') :
            $t('referrerManagement.RegistrationFailure') }}</AlertTitle>
          <AlertDescription class="break-all">{{ resultMsg }}</AlertDescription>
        </Alert>
      </div>
      <Dialog v-model:open="showConfirm">
        <DialogContent>
          <DialogHeader>
            <DialogTitle><span class="flex justify-center items-center text-zinc-300 text-lg">
                <Icon icon="lucide:message-circle-question-mark" class="w-12 h-12 mr-1 text-red-500 break-all" />{{
                  $t('referrerManagement.confirmRegisterDescription') }}
              </span></DialogTitle>
            <hr class="my-2 border-zinc-950" />
            <DialogDescription class="text-zinc-300">
              <p class="py-1 mt-4">
                <span class="text-zinc-500 mr-4">{{ $t('referrerManagement.referrerName') }} :</span> <span
                  class="text-zinc-300 break-all">{{ name }}</span>
              </p>
              <p class="py-1">
                <span class="text-zinc-500 mr-4">{{ $t('referrerManagement.gasFeeRate') }} :</span> <span
                  class="text-zinc-300 mr-2 break-all">{{ btcFeeRate }} </span> sats/Vb
              </p>
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <div class="flex justify-end gap-3">
              <Button @click="confirmRegister" :loading="isLoading" class="w-36">{{ $t('referrerManagement.confirm')
                }}</Button>
              <Button variant="secondary" @click="showConfirm = false" class="w-36">{{ $t('referrerManagement.cancel')
                }}</Button>
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
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Icon } from '@iconify/vue'
import { Alert, AlertTitle, AlertDescription } from '@/components/ui/alert'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { useWalletStore } from '@/store/wallet'
import { useNameManager } from '@/composables/useNameManager'
import { useReferrerManager } from '@/composables/useReferrerManager'

const walletStore = useWalletStore()
const { nameList, isLoadingNames } = useNameManager()
const { addLocalReferrerName } = useReferrerManager()
const isLoading = ref(false)
const showConfirm = ref(false)
const resultMsg = ref('')
const resultSuccess = ref(false)
const name = ref('')

const { btcFeeRate, address } = storeToRefs(walletStore)
function onRegister() {
  if (!name.value) {
    resultMsg.value = '请填写完整信息'
    resultSuccess.value = false
    return
  }
  showConfirm.value = true
}

async function confirmRegister() {
  isLoading.value = true
  resultMsg.value = ''
  showConfirm.value = false
  try {
    const [err, res] = await stp.registerAsReferrer(name.value, btcFeeRate.value)
    if (err) {
      resultMsg.value = err.message || '注册失败'
      resultSuccess.value = false
    } else {
      resultMsg.value = '注册成功！'
      resultSuccess.value = true
      // 使用推荐人管理器保存注册的name到本地存储
      if (address.value) {
        await addLocalReferrerName(address.value, name.value)
      }
      // 清空输入
      name.value = ''
    }
  } catch (e: any) {
    resultMsg.value = e.message || '未知错误'
    resultSuccess.value = false
  } finally {
    isLoading.value = false
  }
}
</script>