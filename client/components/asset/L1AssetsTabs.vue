<template>
  <div class="space-y-4 mb-4">
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
            <!-- <Button size="sm" :to="`/wallet/asset?type=l1_send&p=btc&t=${c.type}&a=${c.id}`">Send</Button> -->
            <Button size="xs" as-child>
              <RouterLink
                :to="`/wallet/asset?type=l1_send&p=btc&t=${c.type}&a=${c.id}`"
              >
                Send
              </RouterLink>
            </Button>
            <Button size="xs" v-if="channel && channel.status === 16" as-child>
              <RouterLink
                :to="`/wallet/asset?type=splicing_in&p=btc&t=${c.type}&a=${c.id}`"
              >
                Splicing In
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
            <Button size="xs" v-if="channel && channel.status === 16" as-child>
              <RouterLink
                :to="`/wallet/asset?type=splicing_in&p=ordx&t=${c.type}&a=${c.id}`"
              >
                Splicing In
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
            <Button size="xs" v-if="channel && channel.status === 16" as-child>
              <RouterLink
                :to="`/wallet/asset?type=splicing_in&p=brc20&t=${c.type}&a=${c.id}`"
              >
                Splicing In
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
            <Button size="xs" as-child v-if="channel && channel.status === 16">
              <RouterLink
                :to="`/wallet/asset?type=splicing_in&p=runes&t=${c.type}&a=${c.id}`"
              >
                Splicing In
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
import { useL1Store, useChannelStore } from '@/store'

const l1Store = useL1Store()
const channelStore = useChannelStore()
const { channel } = storeToRefs(channelStore)
const { plainList, sat20List, brc20List, runesList } = storeToRefs(l1Store)
</script>
