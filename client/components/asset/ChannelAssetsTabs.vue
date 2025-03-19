<template>
  <div class="space-y-4">
    <div>
      <div class="text-lg font-bold">BTC Balance</div>
      <div class="p-2">
        <div
          class="flex py-2 items-center gap-4"
          v-for="(c, k) in plainList"
          :key="c.id"
        >
          <span>Available: {{ c.amount }} Sats</span>
          <div class="flex items-center gap-2">
            <Button size="xs" as-child>
              <RouterLink
                :to="`/wallet/asset?type=unlock&p=btc&t=utxo&a=${c.id}`"
              >
                Unlock
              </RouterLink>
            </Button>
            <Button size="xs" as-child>
              <RouterLink
                :to="`/wallet/asset?type=splicing_out&p=btc&t=utxo&a=${c.id}`"
              >
                Splicing Out
              </RouterLink>
            </Button>
          </div>
        </div>
      </div>
      <Separator />
    </div>
    <div>
      <div class="text-lg font-bold">SAT20</div>
      <div class="p-2">
        <div
          class="flex py-2 items-center gap-4"
          v-for="(c, k) in sat20List"
          :key="c.id"
        >
          <span>{{ c.label }}: {{ c.amount }} </span>
          <div class="flex items-center gap-2">
            <Button size="xs" as-child>
              <RouterLink
                :to="`/wallet/asset?type=unlock&p=ordx&t=${c.type}&a=${c.id}`"
              >
                Unlock
              </RouterLink>
            </Button>
            <Button size="xs" as-child>
              <RouterLink
                :to="`/wallet/asset?type=splicing_out&p=ordx&t=${c.type}&a=${c.id}`"
              >
                Splicing Out
              </RouterLink>
            </Button>
          </div>
        </div>
      </div>
      <Separator />
    </div>
    <div>
      <div class="text-lg font-bold">BRC20</div>
      <div class="p-2">
        <div
          class="flex py-2 items-center gap-4"
          v-for="(c, k) in brc20List"
          :key="c.id"
        >
          <span>{{ c.label }}: {{ c.amount }} </span>
          <div class="flex items-center gap-2">
            <Button size="xs" as-child>
              <RouterLink
                :to="`/wallet/asset?type=unlock&p=brc20&t=${c.type}&a=${c.id}`"
              >
                Unlock
              </RouterLink>
            </Button>
            <Button size="xs" as-child>
              <RouterLink
                :to="`/wallet/asset?type=splicing_out&p=brc20&t=${c.type}&a=${c.id}`"
              >
                Splicing Out
              </RouterLink>
            </Button>
          </div>
        </div>
      </div>
      <Separator />
    </div>
    <div>
      <div class="text-lg font-bold">Runes</div>
      <div class="p-2">
        <div
          class="flex py-2 items-center gap-4"
          v-for="(c, k) in runesList"
          :key="c.id"
        >
          <span>{{ c.label }}: {{ c.amount }} </span>
          <div class="flex items-center gap-2">
            <Button size="xs" as-child>
              <RouterLink
                :to="`/wallet/asset?type=unlock&p=runes&t=${c.type}&a=${c.id}`"
              >
                Unlock
              </RouterLink>
            </Button>
            <Button size="xs" as-child>
              <RouterLink
                :to="`/wallet/asset?type=splicing_out&p=runes&t=${c.type}&a=${c.id}`"
              >
                Splicing Out
              </RouterLink>
            </Button>
          </div>
        </div>
      </div>
      <Separator />
    </div>
  </div>
</template>

<script setup lang="ts">
import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
import { useChannelStore } from '~/store'
const channelStore = useChannelStore()
const { plainList, sat20List, runesList, brc20List } = storeToRefs(channelStore)
</script>
