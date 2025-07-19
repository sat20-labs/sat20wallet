<template>
  <LayoutSecond title="绑定推荐人">
    <div class="max-w-xl mx-auto my-8">
      <div class="mb-4">
        <p class="text-sm text-muted-foreground">
          为服务器绑定推荐人
        </p>
      </div>
      <div class="flex flex-col gap-4">
        <div class="grid w-full items-center gap-1.5">
          <Label for="referrerName">推荐人名称</Label>
          <Input id="referrerName" v-model="referrerName" placeholder="请输入推荐人名称" />
        </div>
        <div class="grid w-full items-center gap-1.5">
          <Label for="serverPubKey">服务器公钥</Label>
          <Input id="serverPubKey" v-model="serverPubKey" placeholder="请输入服务器公钥" />
        </div>
        <Button aria-label="绑定推荐人" @click="onBind" :loading="isLoading">
          绑定推荐人
        </Button>
      </div>
      <div v-if="resultMsg" class="mt-4">
        <Alert :variant="resultSuccess ? 'default' : 'destructive'">
          <AlertTitle>{{ resultSuccess ? '操作成功' : '操作失败' }}</AlertTitle>
          <AlertDescription>{{ resultMsg }}</AlertDescription>
        </Alert>
      </div>
      <Dialog v-model:open="showConfirm">
        <DialogContent>
          <DialogHeader>
            <DialogTitle>确认操作</DialogTitle>
            <DialogDescription>
              确认要绑定推荐人吗？<br>
              推荐人名称：{{ referrerName }}<br>
              服务器公钥：{{ serverPubKey }}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button @click="confirmBind" :loading="isLoading">确认</Button>
            <Button variant="outline" @click="showConfirm = false">取消</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  </LayoutSecond>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import stp from '@/utils/stp'
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert, AlertTitle, AlertDescription } from '@/components/ui/alert'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'

const isLoading = ref(false)
const showConfirm = ref(false)
const resultMsg = ref('')
const resultSuccess = ref(false)
const referrerName = ref('')
const serverPubKey = ref('')

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