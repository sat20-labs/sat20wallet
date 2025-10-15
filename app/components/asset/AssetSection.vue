<template>
  <div class="grid grid-cols-1 gap-2 sm:gap-3">
    <UiSelect
      v-model="params.assetType"
      :options="uniqueAssetList"
      :placeholder="'Select type'"
      @update:modelValue="typeChangeHandler"
    />

    <template v-if="params.assetType">
      <UiSelect
        v-if="params.assetType !== 'btc'"
        v-model="params.asset"
        @update:model-value="assetChangeHandler"
        :options="assetList"
        :placeholder="'Select asset'"
      />
    </template>

    <Input
      v-if="amount"
      :model-value="amount"
      type="number"
      placeholder="0"
      readonly
      disabled
    >
      <template #suffix v-if="params.assetType !== 'runes'"> sats </template>
    </Input>
  </div>
</template>

<script setup lang="ts">
import { Input } from '@/components/ui/input'
import UiSelect from '@/components/shadcn/UiSelect.vue'
import { useRoute } from 'vue-router'
import { computed, reactive, watch, onMounted } from 'vue'

// 导入缺失的 store
import { useL2Store, useL1Store } from '@/store'
import { storeToRefs } from 'pinia'

interface Props {
  type: string
  address?: string
  network?: string
}

const route = useRoute()
const query = route.query
const { t, p, a } = query

const props = withDefaults(defineProps<Props>(), {
  type: '',
  address: '',
  network: 'testnet',
})

const emit = defineEmits(['change'])

const l1Store = useL1Store()
const l2Store = useL2Store()
const {
  uniqueAssetList: l1UniqueAssetList,
  sat20List: l1FtList,
  runesList: l1RunesList,
  brc20List: l1Brc20List,
  plainList: l1PlainList,
} = storeToRefs(l1Store)
const {
  uniqueAssetList: l2UniqueAssetList,
  sat20List: l2FtList,
  brc20List: l2Brc20List,
  runesList: l2RunesList,
  plainList: l2PlainList,
} = storeToRefs(l2Store)

const uniqueAssetList = computed(() => {
  // return props.type === 'lock' ? l2UniqueAssetList.value : l1UniqueAssetList.value
  if (props.type === 'l2_send') {
    return l2UniqueAssetList.value
  } else if (props.type === 'l1_send') {
    return l1UniqueAssetList.value
  } else {
    return l2UniqueAssetList.value // 默认使用L2
  }
})

const plainUtxoList = computed(() => {
  let _list = []
  if (props.type === 'l2_send') {
    _list = l2PlainList.value
  } else if (props.type === 'l1_send') {
    _list = l1PlainList.value
  } else {
    _list = l2PlainList.value // 默认使用L2
  }
  return _list
})
console.log('plainUtxoList', plainUtxoList)

const sat20List = computed(() => {
  if (props.type === 'l2_send') {
    return l2FtList.value
  } else if (props.type === 'l1_send') {
    return l1FtList.value
  } else {
    return l2FtList.value // 默认使用L2
  }
})

const runesList = computed(() => {
  if (props.type === 'l2_send') {
    return l2RunesList.value
  } else if (props.type === 'l1_send') {
    return l1RunesList.value
  } else {
    return l2RunesList.value
  }
})

const brc20List = computed(() => {
  if (props.type === 'l2_send') {
    return l2Brc20List.value
  } else if (props.type === 'l1_send') {
    return l1Brc20List.value
  } else {
    return l2RunesList.value
  }
})

const params = reactive<{
  assetType: string
  asset: string
  plainAsset: string[]
}>({
  assetType: '',
  asset: '',
  plainAsset: [],
})
console.log('params', params)

// 由于 UiSelect 不支持 multiple 属性，创建一个计算属性来处理单选的情况
const selectedPlainAsset = computed({
  get: () => params.plainAsset[0] || '',
  set: (value: string) => {
    params.plainAsset = [value]
  },
})

onMounted(() => {
  if (p) {
    params.assetType = p as string

    if (p === 'btc') {
      params.plainAsset = [plainUtxoList.value[0].id]
    } else {
      params.asset = a as string
    }
  }
})

const typeChangeHandler = (e: any) => {
  console.log('typeChangeHandler', e)
  if (e === 'btc') {
    console.log('plainUtxoList.value', plainUtxoList.value)

    params.plainAsset = [plainUtxoList.value[0].id]
  } else {
    params.plainAsset = []
  }
}

const assetList = computed(() => {
  let _list = []
  if (params.assetType === 'runes') {
    _list = runesList.value
  } else if (params.assetType === 'brc20') {
    _list = brc20List.value
  } else if (params.assetType === 'ordx') {
    _list = sat20List.value
  } else if (params.assetType === 'btc') {
    _list = plainUtxoList.value
  }
  return _list.map((v: any) => ({
    ...v,
    label: v.label,
    value: v.key,
  }))
})

const assets = computed(() => {
  if (params.assetType === 'btc') {
    return assetList.value.filter((v: any) => params.plainAsset.includes(v.id))
  } else {
    return assetList.value.filter((v: any) => params.asset === v.id)
  }
})

watch(assets, () => {
  emit('change', assets.value)
})
const amount = computed(() => {
  return assets.value.reduce(
    (acc: number, cur: any) => acc + Number(cur.amount),
    0
  )
})

const assetChangeHandler = (e: any) => {
  console.log('assetChangeHandler', e)
}
</script>
