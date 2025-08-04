<template>
  <LayoutSecond title="Password">
    <form @submit.prevent="onSubmit">
      <FormField name="oldPassword" v-slot="{ componentField }">
        <FormItem>
          <FormLabel for="oldPassword">旧密码</FormLabel>
          <FormControl>
            <Input id="oldPassword" type="password" autocomplete="current-password" v-bind="componentField"
              aria-label="旧密码" aria-required="true" />
          </FormControl>
          <FormMessage />
        </FormItem>
      </FormField>
      <FormField name="newPassword" v-slot="{ componentField }">
        <FormItem class="mt-4">
          <FormLabel for="newPassword">新密码</FormLabel>
          <FormControl>
            <Input id="newPassword" type="password" autocomplete="new-password" v-bind="componentField" aria-label="新密码"
              aria-required="true" />
          </FormControl>
          <FormMessage />
        </FormItem>
      </FormField>
      <FormField name="confirmPassword" v-slot="{ componentField }">
        <FormItem class="mt-4">
          <FormLabel for="confirmPassword">确认新密码</FormLabel>
          <FormControl>
            <Input id="confirmPassword" type="password" autocomplete="new-password" v-bind="componentField"
              aria-label="确认新密码" aria-required="true" />
          </FormControl>
          <FormMessage />
        </FormItem>
      </FormField>
      <div class="mt-6 flex justify-end">
        <Button class="w-full" :disabled="isLoading" type="submit" aria-label="确认修改">
          <span v-if="isLoading">修改中...</span>
          <span v-else>确认修改</span>
        </Button>
      </div>
    </form>
  </LayoutSecond>
</template>

<script setup lang="ts">
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import { ref } from 'vue'
import { useWalletStore } from '@/store/wallet'
import { hashPassword } from '@/utils/crypto'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import * as z from 'zod'
import walletManager from '@/utils/sat20'
import satsnetStp from '@/utils/stp'
import {
  FormField,
  FormItem,
  FormLabel,
  FormControl,
  FormMessage
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { useToast } from '@/components/ui/toast-new'

const formSchema = toTypedSchema(z.object({
  oldPassword: z.string().min(1, '请输入旧密码'),
  newPassword: z.string().min(6, '新密码至少6位'),
  confirmPassword: z.string().min(1, '请确认新密码'),
}).refine(data => data.newPassword === data.confirmPassword, {
  message: '两次输入的新密码不一致',
  path: ['confirmPassword']
}))

const { handleSubmit } = useForm({
  validationSchema: formSchema
})

const walletStore = useWalletStore()
const { toast } = useToast()
const isLoading = ref(false)

const onSubmit = handleSubmit(async (values) => {
  isLoading.value = true
  try {
    const oldHash = await hashPassword(values.oldPassword)
    const newHash = await hashPassword(values.newPassword)
    const [err, res] = await walletManager.changePassword(oldHash, newHash)
    if (err) {
      toast({ title: '修改失败', description: err?.message || '请检查旧密码是否正确', variant: 'destructive' })
    } else {
      toast({ title: '修改成功', description: '密码已更新', variant: 'success' })
      await walletStore.setPassword(newHash)

    }
  } catch (err: any) {
    toast({ title: '修改失败', description: err?.message || '请检查旧密码是否正确', variant: 'destructive' })
  } finally {
    isLoading.value = false
  }
})
</script>

<style scoped>
.bg-card {
  background: var(--card);
}
</style>