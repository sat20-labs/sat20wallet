<template>
  <Button variant="secondary" @click="deleteWallet" class="w-full h-12"><Icon icon="lucide:trash-2" class="mr-2 h-4 w-4" /> {{ t('settingsUi.deleteWallet') }}</Button>
</template>
<!-- <Icon icon="lucide:chevron-down" class="w-6 h-6 shrink-0 text-foreground/60" /> -->
<script lang="ts" setup>
import { Button } from '@/components/ui/button'
import { useWalletStore, useL1Store, useL2Store } from '@/store'
import { Icon } from '@iconify/vue'
import { useI18n } from 'vue-i18n'
import { confirmPendingBackupRisk } from '@/utils/pendingBackupRisk'

const walletStore = useWalletStore()
const l1Store = useL1Store()
const l2Store = useL2Store()
const { t } = useI18n()

const deleteWallet = async () => {
  if (!window.confirm(t('walletManager.confirmDeleteWallet'))) return
  if (!await confirmPendingBackupRisk(key => t(key), true)) return
  const [err, deleted] = await walletStore.deleteWallet(walletStore.walletId)
  if (err || !deleted) return
  l1Store.reset()
  l2Store.reset()
  window.location.reload()
}
</script>
