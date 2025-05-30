<template>
  <Card v-if="items.length > 0">
    <CardHeader>
      <CardTitle class="text-lg">{{ title }}</CardTitle>
    </CardHeader>
    <CardContent class="space-y-3">
      <div v-for="(item, index) in parsedAssetsInuts" :key="`${title}-item-${index}`"
        class="border p-3 rounded-md bg-muted/50">
        <p class="text-sm font-medium break-all mb-1">
          <span class="font-semibold">{{ $t('txDetail.outpoint') }}:</span> {{ item.Outpoint }}
        </p>
        <p class="text-sm font-medium break-all mb-1">
          <span class="font-semibold">{{ $t('txDetail.value') }}:</span> {{ item.Value }}
        </p>
        <div v-if="item.Assets && item.Assets.length > 0" :class="cn('mt-2 pl-2 border-l-2', borderColorClass)">
          <p class="text-xs font-semibold text-foreground/80 mb-1">{{ $t('txDetail.assets') }}:</p>
          <div v-for="(asset, assetIndex) in item.Assets" :key="`${title}-asset-${index}-${assetIndex}`"
            class="text-xs space-y-0.5">
            <p><span class="font-medium"> {{ asset.label }} ({{
              asset.Amount }})</span></p>
          </div>
        </div>
        <div v-else class="mt-1 pl-2 text-xs text-muted-foreground italic">
          {{ $t('txDetail.noAssets', { title: title.toLowerCase().slice(0, -1) }) }}
        </div>
      </div>
    </CardContent>
  </Card>
</template>

<script setup lang="ts">
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { cn } from '@/lib/utils' // Assuming you have the cn utility from shadcn
import satsnetStp from '@/utils/stp'
// --- Type Definitions (Copied from SignPsbt.vue) ---
interface AssetName {
  Protocol: string;
  Type: string;
  Ticker: string;
}

interface Asset {
  Name: AssetName;
  Amount: string;
  Precision: number;
  BindingSat: number;
  Offsets: any;
  label?: string;
}

interface TxDetailItem {
  UtxoId: number;
  Outpoint: string;
  Value: number;
  PkScript: string;
  Assets: Asset[] | null;
}
// --- End Type Definitions ---


interface Props {
  title: string;
  items: TxDetailItem[];
  borderColorClass: string;
}

const props = defineProps<Props>()

const parsedAssetsInuts = ref<any>([])

watch(props.items, async (newItems = []) => {
  for (const item of newItems) {
    const { Assets = [] } = item
    if (Assets) {
      for (const asset of Assets) {
        const { Name } = asset
        const key = `${Name.Protocol}:${Name.Type}:${Name.Ticker}`
        asset.label = Name.Ticker
      }
    }
    parsedAssetsInuts.value.push(item)
  }
}, { immediate: true })
</script>