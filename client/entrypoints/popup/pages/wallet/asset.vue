<template>
  <LayoutSecond title="Asset">
    <div class="p-2">
      <InputSection :type="type" @submit="submit" :loading="loading" />
    </div>
  </LayoutSecond>
</template>

<script setup lang="ts">
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import { sleep } from 'radash'
import InputSection from '@/components/asset/InputSection.vue'
import { useToast } from '@/components/ui/toast'
import { useChannelStore, useL1Store, useL2Store } from '@/store'
import satsnetStp from '@/utils/stp'

const route = useRoute()
const router = useRouter()
const { toast } = useToast()
const type = route.query!.type as string
const l1Store = useL1Store()
const l2Store = useL2Store()
const channelStore = useChannelStore()

const loading = ref(false)
const { plainList } = storeToRefs(l1Store)
const { channel } = storeToRefs(channelStore)

const handleError = (message: string) => {
  toast({
    title: 'Error',
    description: message,
  })
}

const goBack = () => {
  router.back()
}
const splicingIn = async ({
  chanid,
  utxos,
  amt,
  feeUtxos = [],
  feeRate,
  asset_name,
}: any): Promise<void> => {
  loading.value = true

  const [err, result] = await satsnetStp.splicingIn(
    chanid,
    asset_name,
    utxos,
    feeUtxos,
    feeRate,
    amt
  )

  if (err) {
    loading.value = false
    handleError(err.message)
    return
  }

  await sleep(2000)
  await channelStore.getAllChannels()
  loading.value = false
  goBack()
}
const splicingOut = async ({
  chanid,
  toAddress,
  amt,
  feeRate,
  asset_name,
}: any): Promise<void> => {
  console.log(asset_name)

  loading.value = true
  const feeUtxos = plainList.value?.[0]?.utxos || []
  const [err, result] = await satsnetStp.splicingOut(
    chanid,
    toAddress,
    asset_name,
    feeUtxos,
    feeRate,
    amt
  )

  if (err) {
    loading.value = false
    handleError(err.message)
    return
  }

  await sleep(2000)
  await channelStore.getAllChannels()
  loading.value = false
  goBack()
}
const checkChannel = async () => {
  const chanid = channel.value!.channelId

  const [err, result] = await satsnetStp.getChannelStatus(chanid)
  console.log(result)

  if (err || result !== 16) {
    return false
  }

  return true
}

const unlockUtxo = async ({ chanid, amt, feeUtxos = [], asset_name }: any) => {
  console.log('unlock')
  loading.value = true
  const status = await checkChannel()
  if (!status) {
    toast({
      title: 'error',
      description: 'channel tx has not been confirmed',
    })
    loading.value = false
    return
  }

  loading.value = true

  const [err, result] = await satsnetStp.unlockUtxo(chanid, asset_name, amt, [])
  if (err) {
    toast({
      title: 'error',
      description: err.message,
    })
    loading.value = false
    return
  }
  await sleep(1000)
  await channelStore.getAllChannels()
  loading.value = false

  toast({
    title: 'success',
    description: 'unlock success',
  })
  goBack()
}
const lockUtxo = async ({
  utxos,
  chanid,
  amt,
  feeUtxos = [],
  asset_name,
}: any) => {
  console.log('lock')

  loading.value = true
  const [err, result] = await satsnetStp.lockUtxo(
    chanid,
    asset_name,
    amt,
    utxos,
    feeUtxos
  )
  if (err) {
    toast({
      title: 'error',
      description: err.message,
    })
    loading.value = false
    return
  }
  await sleep(2000)
  await channelStore.getAllChannels()
  loading.value = false

  toast({
    title: 'success',
    description: 'lock success',
  })
  goBack()
}
const l1Send = async ({ toAddress, utxos, amt }: any) => {
  loading.value = true
  const [err, result] = await satsnetStp.sendUtxos(toAddress, utxos, amt)
  if (err) {
    toast({
      title: 'error',
      description: err.message,
    })
    loading.value = false
    return
  }
  await sleep(2000)
  loading.value = false

  toast({
    title: 'success',
    description: 'send success',
  })
  goBack()
}
const l2Send = async ({ toAddress, asset_name, amt }: any) => {
  loading.value = true
  const [err, result] = await satsnetStp.sendAssetsSatsNet(toAddress, asset_name, amt)
  if (err) {
    toast({
      title: 'error',
      description: err.message,
    })
    loading.value = false
    return
  }
  await sleep(2000)
  loading.value = false

  toast({
    title: 'success',
    description: 'send success',
  })
  goBack()
}
const submit = async ({ utxos, amt, assets, feeUtxos, toAddress }: any) => {
  console.log(utxos, amt, assets, feeUtxos, toAddress)
  loading.value = true
  try {
    console.log(channel);
    
    const chainid = channel.value?.channelId
    console.log('chainid: ', chainid)

    if (type === 'splicing_in') {
      await splicingIn({
        chanid: chainid,
        utxos,
        amt,
        feeUtxos,
        feeRate: 1,
        asset_name: assets[0].key,
      })
    } else if (type === 'unlock') {
      await unlockUtxo({
        chanid: chainid,
        amt,
        feeUtxos: [],
        asset_name: assets[0].key,
      })
    } else if (type === 'lock') {
      await lockUtxo({
        chanid: chainid,
        utxos,
        amt,
        feeUtxos,
        asset_name: assets[0].key,
      })
    } else if (type === 'splicing_out') {
      await splicingOut({
        chanid: chainid,
        toAddress: toAddress,
        amt,
        feeUtxos: [],
        feeRate: 1,
        asset_name: assets[0].key,
      })
    } else if (type === 'l2_send') {
      await l2Send({
        toAddress,
        asset_name: assets[0].key,
        amt,
      })
    } else if (type === 'l1_send') {
      await l1Send({
        toAddress,
        utxos,
        amt,
      })
    }
  } catch (error) {
    console.log(error)
  } finally {
    loading.value = false
  }
}
</script>
