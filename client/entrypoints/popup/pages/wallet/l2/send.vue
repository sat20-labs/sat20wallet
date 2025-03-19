<template>
  <div class="">
    <div class="bg-white dark:bg-gray-800 rounded-xl shadow-lg p-6">
      <div class="flex items-center gap-2 mb-6">
        <Icon icon="lucide:send" class="h-6 w-6 text-primary-500" />
        <h2 class="text-xl font-bold">Send Bitcoin</h2>
      </div>

      <form @submit.prevent="sendUtxo" class="space-y-6">
        <FormField v-slot="{ componentField }" name="to">
          <FormItem>
            <FormLabel>Recipient Address or Name</FormLabel>
            <FormControl>
              <Input
                v-model="toAddress"
                placeholder="bc1..."
                icon="lucide:user"
              />
            </FormControl>
            <FormMessage />
          </FormItem>
        </FormField>

        <FormField v-slot="{ componentField }" name="amount">
          <FormItem>
            <FormLabel>Amount</FormLabel>
            <FormControl>
              <Input
                v-model="amount"
                type="number"
                icon="lucide:banknote"
              />
            </FormControl>
            <div class="flex justify-between text-sm">
              <div class="flex items-center gap-1">
                <Icon icon="lucide:scale" class="h-4 w-4 text-gray-500" />
                <span>Available Balance: {{ balance }} Sats</span>
              </div>
              <Button
                size="sm"
                variant="link"
                icon="lucide:arrow-up-circle"
                @click="setMaxAmount"
              >
                Max
              </Button>
            </div>
            <FormMessage />
          </FormItem>
        </FormField>

        <FormField v-slot="{ componentField }" name="feeRate">
          <FormItem>
            <FormLabel>Fee</FormLabel>
            <FormControl>
              <Input
                v-model="feeRate"
                type="number"
                readonly
                icon="lucide:calculator"
              />
            </FormControl>
            <FormMessage />
          </FormItem>
        </FormField>

        <div class="grid grid-cols-2 gap-4">
          <Button
            type="submit"
            :loading="loading"
            class="flex-1 min-w-[120px]"
            icon="lucide:send"
          >
            Confirm Send
          </Button>
          <Button
            @click="goBack"
            variant="outline"
            class="flex-1 min-w-[120px]"
            icon="lucide:x"
          >
            Cancel
          </Button>
        </div>
      </form>
    </div>
  </div>
</template>

<script setup lang="ts">
import { validateBTCAddress } from '~/utils'
import { ordxApi } from '~/apis'
import { sleep } from 'radash'
import satsnetStp from '@/utils/stp'
import { useToast } from '@/components/ui/toast/use-toast'
import { useL2Store } from '@/store'
import {
  FormField,
  FormItem,
  FormLabel,
  FormControl,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { Icon } from '@iconify/vue'

const router = useRouter()
const l2Store = useL2Store()
const { toast } = useToast()

const { plainUtxos, balance } = storeToRefs(l2Store)
const toAddress = ref<string | number>('')
const amount = ref<string | number>('')
const feeRate = ref<string | number>('10')
const sending = ref(false)
const errors = reactive({
  to: '',
  amount: '',
  feeRate: '',
})

const loading = computed(() => sending.value)
const validate = () => {
  errors.to = ''
  errors.amount = ''
  errors.feeRate = ''
  console.log(validateBTCAddress(toAddress.value as string))

  // if (!validateBTCAddress(toAddress.value as string)) {
  //   errors.to = 'Invalid Bitcoin address'
  //   return false
  // }

  if (!amount.value || Number(amount.value) <= 0) {
    errors.amount = 'Amount must be greater than 0'
    return false
  }

  if (parseInt(amount.value as string) > balance.value) {
    errors.amount = 'Insufficient balance'
    return false
  }


  return true
}

const sendUtxo = async () => {
  console.log('send')
  sending.value = true
  const spendUtxos = plainUtxos.value?.map((v) => `${v.txid}:${v.vout}`)
  const validSatus = validate()

  if (!validSatus) {
    sending.value = false
    return
  }
  console.log(amount.value)
  let amt = parseInt(amount.value as string)
  if (amt === balance.value) {
    amt = 0
  }
  let toAdd = toAddress.value
  try {
    const nsRes = await ordxApi.getNsName({
      name: toAddress.value,
      network: 'testnet',
    })

    if (nsRes?.data?.address) {
      toAdd = nsRes.data.address
    }
  } catch (error) {
    console.log(error)
  }

  const [err, result] = await satsnetStp.sendUtxos(
    toAdd as string,
    spendUtxos,
    Number(amt)
  )
  await sleep(3000)
  sending.value = false
  if (err) {
    toast({
      title: 'Error',
      description: err.message,
      variant: 'destructive',
    })
    return
  } else {
    toast({
      title: 'Success',
      description: 'Send successful',
    })
  }
  goBack()
}

const setMaxAmount = () => {
  amount.value = balance.value?.toString() || ''
}

const goBack = () => {
  router.back()
}
</script>
