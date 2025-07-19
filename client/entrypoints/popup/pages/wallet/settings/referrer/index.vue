<template>
  <LayoutSecond title="注册推荐人">
    <div class="max-w-xl mx-auto my-8">
      <div class="flex flex-col gap-4">
        <div class="mb-4">
          <p class="text-sm text-muted-foreground">成为推荐人可以获得相应的收益分成</p>
        </div>
        <div class="grid w-full items-center gap-1.5">
          <Label for="name">推荐人名称</Label>
          <Input id="name" v-model="name" placeholder="请输入推荐人名称" />
        </div>
        <div class="grid w-full items-center gap-1.5">
          <Label for="feeRate">费率（sats/Vb）</Label>
          <Input id="feeRate" v-model="btcFeeRate" type="number" min="0" max="100" placeholder="请输入费率" />
        </div>
        <Button aria-label="注册推荐人" @click="onRegister" :loading="isLoading">
          注册推荐人
        </Button>
        <Alert v-if="resultMsg" :variant="resultSuccess ? 'default' : 'destructive'">
          <AlertTitle>{{ resultSuccess ? '操作成功' : '操作失败' }}</AlertTitle>
          <AlertDescription class="break-all">{{ resultMsg }}</AlertDescription>
        </Alert>
      </div>
      <Dialog v-model:open="showConfirm">
        <DialogContent>
          <DialogHeader>
            <DialogTitle>确认操作</DialogTitle>
            <DialogDescription>
              确认要注册为推荐人吗？<br>
              推荐人名称：{{ name }}<br>
              费率：{{ btcFeeRate }} sats/Vb
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button @click="confirmRegister" :loading="isLoading">确认</Button>
            <Button variant="outline" @click="showConfirm = false">取消</Button>
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
import { useWalletStore } from '@/store/wallet'

const walletStore = useWalletStore()
const isLoading = ref(false)
const showConfirm = ref(false)
const resultMsg = ref('')
const resultSuccess = ref(false)
const name = ref('')

const { btcFeeRate } = storeToRefs(walletStore)
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