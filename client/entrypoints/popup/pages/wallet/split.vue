<template>
  <LayoutSecond :title="$t('splitAsset.confirmTitle')">
    <div class="mb-6">
      <p class="text-muted-foreground">{{ $t('splitAsset.description') }}</p>
      <div class="text-sm text-foreground mt-2">
        {{$t('splitAsset.balance')}}: <span class="font-bold">{{ balance.availableAmt }}</span>
      </div>
    </div>
    <form @submit="onSubmit">
      <div class="space-y-4">
        <FormField v-slot="{ componentField }" name="assetName">
          <FormItem>
            <FormLabel>{{ $t('splitAsset.assetKey') }}</FormLabel>
            <FormControl>
              <Input type="text" :placeholder="$t('splitAsset.assetKey')" class="h-12 bg-zinc-800" v-bind="componentField" disabled />
            </FormControl>
            <FormMessage />
          </FormItem>
        </FormField>
        <FormField v-slot="{ componentField }" name="destAddr">
          <FormItem>
            <FormLabel>{{ $t('assetOperationDialog.address') }}</FormLabel>
            <FormControl>
              <Input type="text" :placeholder="$t('assetOperationDialog.enterAddress')" class="h-12 bg-zinc-800" v-bind="componentField" />
            </FormControl>
            <FormMessage />
          </FormItem>
        </FormField>
        <FormField v-slot="{ componentField }" name="amt">
          <FormItem>
            <FormLabel>{{ $t('assetOperationDialog.amount') }}</FormLabel>
            <FormControl>
              <Input type="number" :placeholder="$t('assetOperationDialog.enterAmount')" class="h-12 bg-zinc-800" v-bind="componentField" />
            </FormControl>
            <FormMessage />
          </FormItem>
        </FormField>
        <FormField v-slot="{ componentField }" name="n">
          <FormItem>
            <FormLabel>{{ $t('splitAsset.repeat') }}</FormLabel>
            <FormControl>
              <Input type="number" :placeholder="$t('splitAsset.repeatPlaceholder')" class="h-12 bg-zinc-800" v-bind="componentField" />
            </FormControl>
            <FormMessage />
          </FormItem>
        </FormField>
        <p v-if="errorMessage" class="text-sm text-destructive">{{ errorMessage }}</p>
      </div>
      <div class="mt-6">
        <Button class="w-full h-11 mb-2" :loading="loading" type="submit">
          {{ $t('assetOperationDialog.confirm') }}
        </Button>
      </div>
    </form>
  </LayoutSecond>
</template>

<script lang="ts" setup>
import { ref, onMounted, reactive, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import { useWalletStore } from '@/store'
import { useL2Assets } from '@/composables/hooks/useL2Assets'
import { useToast } from '@/components/ui/toast/use-toast'
import satsnetStp from '@/utils/stp'
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Form, FormItem, FormLabel, FormControl, FormMessage, FormField } from '@/components/ui/form'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import * as z from 'zod'

const isOpen = ref(true)
const route = useRoute()
const router = useRouter()

const formInitialValues = reactive({
  assetName: '',
  amt: '',
  n: '',
  destAddr: ''
})
const loading = ref(false)
const errorMessage = ref<string | null>(null)

const { address, btcFeeRate } = storeToRefs(useWalletStore())
const { refreshL2Assets, loading: l2Loading } = useL2Assets()
const balance = ref<{
  availableAmt: number,
  lockedAmt: number
}>({
  availableAmt: 0,
  lockedAmt: 0
})
const { toast } = useToast()

const fetchAssetBalance = async () => {
  if (!route.query.assetName || !address.value) return;
  const [err, result] = await satsnetStp.getAssetAmount(address.value, route.query.assetName as string)
  if (result) {
    balance.value.availableAmt = result.availableAmt
    balance.value.lockedAmt = result.lockedAmt
  }
}

watch([route.query.assetName, address], () => {
  fetchAssetBalance()
  formInitialValues.assetName = route.query.assetName as string || ''
  formInitialValues.destAddr = address.value || ''
}, { immediate: true, deep: true })



const splitSchema = z.object({
  assetName: z.string().min(1, 'Asset name is required'),
  amt: z.preprocess((v) => Number(v), z.number().positive('Amount must be positive')),
  n: z.preprocess((v) => Number(v), z.number().int().positive('n must be positive')),
  destAddr: z.string().min(1, 'Address is required'),
})

const form = useForm({
  validationSchema: toTypedSchema(splitSchema),
  initialValues: formInitialValues
})

const onSubmit = form.handleSubmit(async (values) => {
  errorMessage.value = null
  loading.value = true
  try {
    const [err, result] = await satsnetStp.batchSendAssets(
      values.destAddr,
      values.assetName,
      values.amt.toString(),
      Number(values.n),
      0
    )
    if (err) {
      let detail = 'L2资产拆分失败。'
      if (err.message) detail = err.message
      else if (typeof err === 'string') detail = err
      throw new Error(detail)
    }
    toast({
      title: '成功',
      description: `成功发起拆分：${values.amt} ${values.assetName}`,
    })
    await refreshL2Assets()
    isOpen.value = false
    setTimeout(() => router.back(), 300)
  } catch (error: any) {
    console.error('L2 Split Error:', error)
    const description = error.message || '拆分过程中发生未知错误。'
    toast({ title: '错误', description, variant: 'destructive' })
    errorMessage.value = description
  } finally {
    loading.value = false
  }
})
</script>
