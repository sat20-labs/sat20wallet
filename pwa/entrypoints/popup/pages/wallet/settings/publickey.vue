<template>
  <LayoutSecond :title="t('settingsUi.publicKeyTitle')">
    <div class="max-w-xl mx-auto mt-6">
      <div class="flex items-center gap-2 mb-4">
        <div class="flex-1 break-all select-all text-sm text-muted-foreground">
          {{ publicKey || t('settingsUi.noPublicKey') }}
        </div>
        <CopyButton :text="publicKey" v-if="publicKey" />
      </div>
      <div class="space-y-2">
        <Label for="peer-pubkey">{{ t('settingsUi.peerPublicKey') }}</Label>
        <Input id="peer-pubkey" v-model="peerPubkeyInput" :placeholder="t('settingsUi.peerPlaceholder')" autocomplete="off" />
        <Button class="w-full mt-2" :disabled="!peerPubkeyInput || loading" @click="calcChannelPubkey">
          {{ loading ? t('settingsUi.calculating') : t('settingsUi.calculateChannelKey') }}
        </Button>
        <Alert v-if="error" variant="destructive" class="mt-2">
          {{ error }}
        </Alert>
        <div v-if="channelAddr || peerAddr" class="mt-2 space-y-2">
          <div class="flex items-center gap-2">
            <span class="font-semibold">{{ t('settingsUi.channelAddress') }}</span>
            <span class="break-all select-all text-sm">{{ channelAddr }}</span>
            <CopyButton :text="channelAddr" v-if="channelAddr" />
          </div>
          <div class="flex items-center gap-2">
            <span class="font-semibold">{{ t('settingsUi.serviceNodeAddress') }}</span>
            <span class="break-all select-all text-sm">{{ peerAddr }}</span>
            <CopyButton :text="peerAddr" v-if="peerAddr" />
          </div>
        </div>
      </div>
    </div>
  </LayoutSecond>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
const { t } = useI18n()
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import { ref, computed } from 'vue'
import { useWalletStore } from '@/store'
import walletManager from '@/utils/sat20'
import Card from '@/components/ui/card/Card.vue'
import CardHeader from '@/components/ui/card/CardHeader.vue'
import CardTitle from '@/components/ui/card/CardTitle.vue'
import CardContent from '@/components/ui/card/CardContent.vue'
import Label from '@/components/ui/label/Label.vue'
import Input from '@/components/ui/input/Input.vue'
import Button from '@/components/ui/button/Button.vue'
import Alert from '@/components/ui/alert/Alert.vue'
import CopyButton from '@/components/common/CopyButton.vue'

// 获取当前用户 publicKey
const walletStore = useWalletStore()
const publicKey = computed(() => walletStore.publicKey)

// 输入框、结果、loading、错误
const peerPubkeyInput = ref('')
const channelAddr = ref('')
const peerAddr = ref('')
const loading = ref(false)
const error = ref('')

// 计算通道公钥
async function calcChannelPubkey() {
  error.value = ''
  channelAddr.value = ''
  peerAddr.value = ''
  loading.value = true
  try {
    const peerPubkey = peerPubkeyInput.value.trim()
    const [err, result] = await walletManager.getChannelAddrByPeerPubkey(peerPubkey)
    if (err || !result) {
      throw new Error(err?.message || t('settingsUi.calculationFailed'))
    }
    channelAddr.value = result.channelAddr
    peerAddr.value = result.peerAddr
  } catch (e: any) {
    error.value = e.message || t('settingsUi.unknownError')
  } finally {
    loading.value = false
  }
}
</script>