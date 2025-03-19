<template>
  <div class="space-y-4 mb-4">
    <div>
      <h3 class="text-base sm:text-lg font-semibold">Add Asset</h3>
      <div class="p-2 space-y-3 sm:space-y-4">
        <div v-for="(item, i) in inputList" :key="i">
          <div class="space-y-2 sm:space-y-3">
            <div class="flex items-center justify-between">
              <p class="text-sm sm:text-base font-medium">Asset {{ i + 1 }}</p>
              <!-- <div class="flex space-x-1 sm:space-x-2">
                  <Button
                    variant="soft"
                    size="icon"
                    @click="addItem"
                  >
                    <PlusIcon class="h-4 w-4" />
                  </Button>
                  <Button
                    v-if="inputList.length > 1"
                    variant="destructive"
                    size="icon"
                    @click="removeItem(item.id)"
                  >
                    <MinusIcon class="h-4 w-4" />
                  </Button>
                </div> -->
            </div>
            <AssetSection
              :type="type"
              @change="(e: any) => assetChange(item.id, e)"
            />
          </div>
        </div>
      </div>
    </div>
    <FormItem label="To Address" v-if="showAddress">
      <Input
        v-model="toAddress"
        class="dark:bg-gray-800"
        placeholder="tbc1..."
      />
    </FormItem>
    <FormItem label="Total Amount">
      <Input
        v-model="totalAmount"
        type="number"
        class="dark:bg-gray-800"
        placeholder="0"
      >
        <template #suffix v-if="asset?.protocol !== 'runes'"> sats </template>
      </Input>
    </FormItem>
  </div>
  <div class="flex justify-end">
    <Button
      :disabled="loading || isLoading"
      class="w-full"
      variant="default"
      @click="submit"
    >
      <Loader2Icon
        v-if="loading || isLoading"
        class="mr-2 h-4 w-4 animate-spin"
      />
      Submit
    </Button>
  </div>
</template>

<script setup lang="ts">
import { ordxApi } from '~/apis'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FormItem } from '@/components/ui/form'
import { PlusIcon, MinusIcon, Loader2Icon } from 'lucide-vue-next'
import AssetSection from './AssetSection.vue'
import { useL2Store, useL1Store, useChannelStore } from '@/store'

interface Props {
  type: string
  loading: boolean
}

const props = withDefaults(defineProps<Props>(), {
  type: '',
  address: '',
  network: 'testnet',
})
const emit = defineEmits(['submit'])

const { type } = toRefs(props)
const isLoading = ref(false)
const inputList = ref<
  {
    id: number
    assets: any[]
  }[]
>([
  {
    id: 0,
    assets: [],
  },
])
const l1Store = useL1Store()
const l2Store = useL2Store()
const channelStore = useChannelStore()

const { plainList: l1PlainList } = storeToRefs(l1Store)
const { plainList: l2PlainList } = storeToRefs(l2Store)
const { plainList: channelPlainList } = storeToRefs(channelStore)

const plainList = computed(() => {
  if (type.value === 'lock') {
    return l2PlainList.value
  } else if (type.value === 'splicing_in') {
    return l1PlainList.value
  } else {
    return channelPlainList.value
  }
})

const assetAmount = computed(() => {
  return inputList.value.reduce((acc, cur) => {
    return acc + cur.assets.reduce((acc, cur) => acc + Number(cur.amount), 0)
  }, 0)
})

const asset = computed(() => {
  return inputList.value[0]?.assets?.[0]
})
console.log('asset', asset.value)

const totalAmount = ref<string | number>('')
const toAddress = ref<string | number>('')
const showAddress = computed(() =>
  ['l1_send', 'l2_send', 'splicing_out'].includes(props.type)
)
const addItem = () => {
  inputList.value.push({
    id: inputList.value.length,
    assets: [],
  })
}

const removeItem = (id: number) => {
  const index = inputList.value.findIndex((item) => item.id === id)
  if (index !== -1) {
    inputList.value.splice(index, 1)
  }
}
const assetChange = (id: number, e: any[]) => {
  if (e?.length) {
    const index = inputList.value.findIndex((item) => item.id === id)
    if (index !== -1) {
      inputList.value[index].assets = e
    }
    const amt = e.reduce((acc: number, cur: any) => acc + Number(cur.amount), 0)
    totalAmount.value = amt.toString()
  }
}

const submit = async () => {
  const amt = Number(totalAmount.value)
  const assets = inputList.value.reduce((acc, cur: any) => {
    return acc.concat(cur.assets)
  }, [])
  let toAdd = toAddress.value
  if (showAddress.value) {
    isLoading.value = true
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
    isLoading.value = false
  }
  console.log('inputList: ', inputList.value)

  const assetUtxos: string[] = inputList.value.reduce((acc, cur: any) => {
    const cUtxos = cur.assets.map((v: any) => v.utxos)
    return acc.concat(...cUtxos)
  }, [])

  const plainUtxos: any[] = []
  console.log('plainList', plainList.value)
  console.log('assetUtxos', assetUtxos)

  plainList.value?.[0]?.utxos?.forEach((v: any) => {
    if (!assetUtxos.includes(v)) {
      plainUtxos.push(v)
    }
  })
  console.log('plainUtxos', plainUtxos)

  const utxoList = [...assetUtxos]
  emit('submit', {
    assets,
    amt,
    utxos: utxoList,
    feeUtxos: toRaw(plainUtxos),
    toAddress: toAdd,
  })
}
</script>
