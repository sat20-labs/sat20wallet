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
            <Button size="xs">
              <RouterLink
                :to="`/wallet/asset?type=l2_send&p=btc&t=${c.type}&a=${c.id}`"
              >
                Send
              </RouterLink>
            </Button>
            <Button v-if="channel && channel.status === 16" size="xs">
              <RouterLink
                :to="`/wallet/asset?type=lock&p=btc&t=${c.type}&a=${c.id}`"
              >
                Lock
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
                :to="`/wallet/asset?type=l2_send&p=ordx&t=${c.type}&a=${c.id}&l=l2`"
              >
                Send
              </RouterLink>
            </Button>
            <Button v-if="channel && channel.status === 16" size="xs" as-child>
              <RouterLink
                :to="`/wallet/asset?type=lock&p=ordx&t=${c.type}&a=${c.id}`"
              >
                Lock
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
                :to="`/wallet/asset?type=l2_send&p=brc20&t=${c.type}&a=${c.id}&l=l2`"
              >
                Send
              </RouterLink>
            </Button>
            >
            <Button v-if="channel && channel.status === 16" size="xs" as-child>
              <RouterLink
                :to="`/wallet/asset?type=lock&p=brc20&t=${c.type}&a=${c.id}`"
              >
                Lock
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
                :to="`/wallet/asset?type=l2_send&p=runes&t=${c.type}&a=${c.id}&l=l2`"
              >
                Send
              </RouterLink>
            </Button>
            <Button v-if="channel && channel.status === 16" size="xs" as-child>
              <RouterLink
                :to="`/wallet/asset?type=lock&p=runes&t=${c.type}&a=${c.id}`"
              >
                Lock
              </RouterLink>
            </Button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
import { useL2Store, useChannelStore } from '@/store'

const l2Store = useL2Store()
const channelStore = useChannelStore()
const { channel } = storeToRefs(channelStore)
const { runesList, plainList, sat20List, brc20List } = storeToRefs(l2Store)
</script>
