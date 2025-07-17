<template>
  <div class="layout-container h-screen flex flex-col">
    <!-- Header -->
    <header class="flex-none z-40 flex items-center justify-between p-4 border-b">
      <div class="flex items-center gap-2">
        <Button variant="ghost" size="icon" @click="router.back()">
          <Icon icon="lucide:arrow-left" class="w-5 h-5" />
        </Button>
        <h1 class="text-lg font-semibold">{{ $t('walletManager.title') }}</h1>
      </div>
    </header>

    <!-- Scrollable Content -->
    <main class="flex-1 overflow-y-auto">
      <div class="container max-w-2xl mx-auto p-4 space-y-6">
        <div class="space-y-4">

          <!-- Wallet List -->
          <div class="space-y-2">
            <div v-for="wallet in wallets" :key="wallet.id"
              class="flex items-center justify-between p-3 rounded-lg border hover:bg-accent/50 transition-colors"
              :class="{ 'border-primary/50': wallet.id === currentWalletId }">
              <div class="flex items-center gap-3">
                <Button variant="ghost" size="icon" class="w-10 h-10 p-0 rounded-full overflow-hidden"
                  @click="showAvatarDialog(wallet)" v-if="wallet.id === currentWalletId">
                  <img v-if="wallet.avatar" :src="wallet.avatar" :alt="wallet.name"
                    class="w-full h-full object-cover" />
                  <div v-else class="w-full h-full flex items-center justify-center bg-muted/80">
                    <Icon icon="lucide:wallet" class="w-5 h-5 text-white/60" />
                  </div>
                </Button>
                <div v-else class="w-10 h-10 rounded-full overflow-hidden">
                  <img v-if="wallet.avatar" :src="wallet.avatar" :alt="wallet.name"
                    class="w-full h-full object-cover" />
                  <div v-else class="w-full h-full flex items-center justify-center bg-muted/80">
                    <Icon icon="lucide:wallet" class="w-5 h-5 text-white/60" />
                  </div>
                </div>
                <div>
                  <div class="font-medium flex items-center gap-2 text-white/60">
                    {{ wallet.name }}
                    <Button v-if="wallet.id === currentWalletId" variant="ghost" size="icon" class="h-2 w-2"
                      @click="showEditNameDialog(wallet)">
                      <Icon icon="lucide:pencil" class="w-2 h-2" />
                    </Button>
                  </div>
                </div>
              </div>
              <div class="flex items-center gap-2">
                <Button v-if="wallet.id !== currentWalletId" variant="outline" size="sm" @click="selectWallet(wallet)">
                  {{ $t('walletManager.switch') }}
                </Button>
                <Button v-else variant="outline" size="sm" disabled>
                  {{ $t('walletManager.current') }}
                </Button>
                <Button v-if="wallet.id !== currentWalletId" variant="ghost" size="icon"
                  class="text-destructive hover:text-destructive" @click="confirmDeleteWallet(wallet)">
                  <Icon icon="lucide:trash-2" class="w-4 h-4" />
                </Button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </main>

    <!-- Bottom Buttons -->
    <footer class="flex-none z-40 border-t">
      <div class="container max-w-2xl mx-auto p-4">
        <div class="flex gap-2">
          <Button class="flex-1 gap-2 h-11 flex items-center" variant="default" @click="createWallet">
            <Icon icon="lucide:plus-circle" class="w-6 h-6 flex-shrink-0" />
            {{ $t('walletManager.createWallet') }}
          </Button>
          <Button class="flex-1 gap-2 h-11" variant="secondary" @click="showImportWalletDialog">
            <Icon icon="lucide:import" class="w-6 h-6 flex-shrink-0" />
            {{ $t('walletManager.importWallet') }}
          </Button>
        </div>
      </div>
    </footer>

    <!-- Edit Name Dialog -->
    <Dialog :open="isEditNameDialogOpen" @update:open="isEditNameDialogOpen = $event">
      <DialogContent class="sm:max-w-[425px]">
        <DialogHeader>
          <DialogTitle>{{ $t('walletManager.editWalletName') }}</DialogTitle>
          <DialogDescription>
            <hr class="mb-6 mt-1 border-t-1 border-accent">
            {{ $t('walletManager.changeWalletName') }}
          </DialogDescription>
        </DialogHeader>
        <div class="space-y-4">
          <div class="space-y-2">
            <Label for="walletName">{{ $t('walletManager.walletName') }}</Label>
            <Input id="walletName" v-model="editingName" :placeholder="$t('walletManager.enterWalletName')" />
          </div>
        </div>
        <DialogFooter>
          <Button variant="secondary" @click="isEditNameDialogOpen = false" class="h-11 mt-2">
            {{ $t('walletManager.cancel') }}
          </Button>
          <Button :disabled="isEditingName" @click="saveWalletName" class="h-11 mt-2">
            <Icon v-if="isEditingName" icon="lucide:loader-2" class="w-4 h-4 mr-2 animate-spin" />
            {{ isEditingName ? $t('walletManager.saving') : $t('walletManager.save') }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- Avatar Dialog -->
    <Dialog :open="isAvatarDialogOpen" @update:open="isAvatarDialogOpen = $event">
      <DialogContent class="sm:max-w-[425px]">
        <DialogHeader>
          <DialogTitle>{{ $t('walletManager.changeAvatar') }}</DialogTitle>
          <DialogDescription>
            <hr class="mb-6 mt-1 border-t-1 border-accent">
            {{ $t('walletManager.chooseAvatar') }}
          </DialogDescription>
        </DialogHeader>
        <div class="space-y-4">
          <Tabs defaultValue="nfts" class="w-full">
            <TabsList class="grid w-full grid-cols-2">
              <TabsTrigger value="nfts">{{ $t('walletManager.nfts') }}</TabsTrigger>
              <TabsTrigger value="domains">{{ $t('walletManager.btcDomains') }}</TabsTrigger>
            </TabsList>
            <TabsContent value="nfts">
              <div class="grid grid-cols-3 gap-2">
                <Button v-for="nft in nfts" :key="nft.id" variant="outline" class="aspect-square p-0 relative group"
                  @click="selectAvatar(nft.image)">
                  <img :src="nft.image" :alt="nft.name" class="w-full h-full object-cover rounded-lg" />
                  <div
                    class="absolute inset-0 bg-black/50 flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity">
                    <Icon icon="lucide:check" class="w-6 h-6 text-white" />
                  </div>
                </Button>
              </div>
            </TabsContent>
            <TabsContent value="domains">
              <div class="grid grid-cols-3 gap-2">
                <Button v-for="domain in btcDomains" :key="domain.id" variant="outline"
                  class="aspect-square p-0 relative group" @click="selectAvatar(domain.image)">
                  <img :src="domain.image" :alt="domain.name" class="w-full h-full object-cover rounded-lg" />
                  <div
                    class="absolute inset-0 bg-black/50 flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity">
                    <Icon icon="lucide:check" class="w-6 h-6 text-white" />
                  </div>
                </Button>
              </div>
            </TabsContent>
          </Tabs>
        </div>
        <DialogFooter>
          <Button variant="secondary" @click="isAvatarDialogOpen = false" class="h-11 mt-2">
            {{ $t('walletManager.cancel') }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- Import Wallet Dialog -->
    <Dialog :open="isImportWalletDialogOpen" @update:open="isImportWalletDialogOpen = $event">
      <DialogContent class="sm:max-w-[425px]">
        <DialogHeader>
          <DialogTitle>{{ $t('walletManager.importWallet') }}</DialogTitle>
          <DialogDescription>
            <hr class="mb-6 mt-1 border-t-1 border-accent">
            {{ $t('walletManager.importWalletDescription') }}
          </DialogDescription>
        </DialogHeader>
        <form @submit.prevent="importWallet" class="space-y-4">
          <div class="space-y-4">
            <div class="space-y-2">
              <Label for="mnemonic">{{ $t('walletManager.recoveryPhrase') }}</Label>
              <Textarea id="mnemonic" v-model="importMnemonic" :placeholder="$t('walletManager.enterRecoveryPhrase')"
                rows="3" />
              <p class="text-xs text-muted-foreground">
                {{ $t('walletManager.recoveryPhraseHint') }}
              </p>
            </div>
          </div>
          <DialogFooter>
            <Button variant="secondary" type="button" @click="isImportWalletDialogOpen = false" class="h-11 mt-2">
              {{ $t('walletManager.cancel') }}
            </Button>
            <Button type="submit" :disabled="isImporting" class="h-11 mt-2">
              <Icon v-if="isImporting" icon="lucide:loader-2" class="w-4 h-4 mr-2 animate-spin" />
              {{ isImporting ? $t('walletManager.importing') : $t('walletManager.import') }}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>


    <!-- Delete Confirmation Dialog -->
    <Dialog :open="isDeleteDialogOpen" @update:open="isDeleteDialogOpen = $event">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ $t('walletManager.deleteWallet') }}</DialogTitle>
          <DialogDescription>
            <hr class="mb-6 mt-1 border-t-1 border-accent">
            {{ $t('walletManager.confirmDeleteWallet') }}
          </DialogDescription>
        </DialogHeader>
        <div class="space-y-4">
          <Alert variant="destructive">
            <Icon icon="lucide:alert-triangle" class="w-4 h-4" />
            <AlertDescription>
              {{ $t('walletManager.backupRecoveryPhrase') }}
            </AlertDescription>
          </Alert>
        </div>
        <DialogFooter>
          <Button variant="secondary" @click="isDeleteDialogOpen = false" class="h-11 mt-2">
            {{ $t('walletManager.cancel') }}
          </Button>
          <Button variant="default" :disabled="isDeleting" @click="deleteWallet" class="h-11 mt-2">
            <Icon v-if="isDeleting" icon="lucide:loader-2" class="w-4 h-4 mr-2 animate-spin" />
            {{ isDeleting ? $t('walletManager.deleting') : $t('walletManager.delete') }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Icon } from '@iconify/vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Label } from '@/components/ui/label'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { useToast } from '@/components/ui/toast/use-toast'
import { useWalletStore } from '@/store'
import { WalletData } from '@/types'
import { Message } from '@/types/message'
import { sendAccountsChangedEvent } from '@/lib/utils'
import walletManager from '@/utils/sat20'
import { hideAddress } from '@/utils'


const router = useRouter()
const walletStore = useWalletStore()
const { wallets } = storeToRefs(walletStore)
const { toast } = useToast()

// State
const isImportWalletDialogOpen = ref(false)
const isDeleteDialogOpen = ref(false)
const isEditNameDialogOpen = ref(false)
const isAvatarDialogOpen = ref(false)
const isCreating = ref(false)
const isImporting = ref(false)
const isDeleting = ref(false)
const isEditingName = ref(false)

const importMnemonic = ref('')
const importPassword = ref('')
const importConfirmPassword = ref('')
const walletToDelete = ref<WalletData | null>(null)
const editingWallet = ref<WalletData | null>(null)
const editingName = ref('')

// Mock NFTs and BTC Domains data
const nfts = ref([
  { id: 1, name: 'NFT 1', image: 'https://picsum.photos/200' },
  { id: 2, name: 'NFT 2', image: 'https://picsum.photos/201' },
  { id: 3, name: 'NFT 3', image: 'https://picsum.photos/202' },
])

const btcDomains = ref([
  { id: 1, name: 'domain1.btc', image: 'https://picsum.photos/203' },
  { id: 2, name: 'domain2.btc', image: 'https://picsum.photos/204' },
  { id: 3, name: 'domain3.btc', image: 'https://picsum.photos/205' },
])

// 计算属性
const currentWalletId = computed(() => walletStore.walletId)

const walletsWithAddress = ref<any[]>([])
const isLoadingWallets = ref(false)

watch(wallets, async (newWallets) => {
  if (!newWallets) {
    walletsWithAddress.value = []
    return
  }
  isLoadingWallets.value = true
  try {
    const results = await Promise.all(
      newWallets.map(async (wallet) => {
        let address = ''
        try {
          const [_, addressRes] = await walletManager.getWalletAddress(0)
          address = addressRes?.address || ''
        } catch { }
        return {
          ...wallet,
          address,
        }
      })
    )
    walletsWithAddress.value = results
  } finally {
    isLoadingWallets.value = false
  }
}, { immediate: true })

const selectWallet = async (wallet: WalletData) => {
  try {
    await walletStore.switchWallet(wallet.id)
    toast({
      title: 'Success',
      description: 'Wallet switched successfully'
    })
    setTimeout(() => {
      router.go(-1)
    }, 300)
    sendAccountsChangedEvent(wallets.value)

  } catch (error: any) {
    console.error('Failed to switch wallet:', error)
    toast({
      variant: 'destructive',
      title: 'Error',
      description: error.message || 'Failed to switch wallet'
    })
  }
}

const confirmDeleteWallet = (wallet: WalletData) => {
  walletToDelete.value = wallet
  isDeleteDialogOpen.value = true
}

const deleteWallet = async () => {
  if (!walletToDelete.value || isDeleting.value) return

  try {
    isDeleting.value = true
    const [err] = await walletStore.deleteWallet(walletToDelete.value.id)

    if (err) {
      throw err
    }

    toast({
      title: 'Success',
      description: 'Wallet deleted successfully'
    })
    isDeleteDialogOpen.value = false
    walletToDelete.value = null
    setTimeout(() => {
      sendAccountsChangedEvent(wallets.value)
    }, 200);
    // 发送 accountsChanged 事件（封装函数）
  } catch (error: any) {
    console.log(error);
    
    toast({
      variant: 'destructive',
      title: 'Error',
      description: error.message || 'Failed to delete Wallet'
    })
  } finally {
    console.log('finished ');
    
    isDeleting.value = false
  }
}


const showImportWalletDialog = () => {
  importMnemonic.value = ''
  importPassword.value = ''
  importConfirmPassword.value = ''
  isImportWalletDialogOpen.value = true
}

const createWallet = async () => {
  if (isCreating.value) return

  try {
    const localPassword = walletStore.password
    if (!localPassword) {
      throw new Error('No password set')
    }
    const [err, result] = await walletStore.createWallet(localPassword)

    if (err || !result) {
      throw err || new Error('Failed to create wallet')
    }
    await walletStore.switchWallet(walletStore.walletId)
    toast({
      title: 'Success',
      description: 'Wallet created successfully'
    })
    setTimeout(() => {
      router.go(-1)
    }, 300)
    sendAccountsChangedEvent(wallets.value)
  } catch (error: any) {
    toast({
      variant: 'destructive',
      title: 'Error',
      description: error.message || 'Failed to create wallet'
    })
  } finally {
    isCreating.value = false
  }
}

const importWallet = async () => {
  if (isImporting.value) return

  try {

    if (!importMnemonic.value) {
      throw new Error('Please enter your recovery phrase')
    }
    isImporting.value = true
    const localPassword = walletStore.password
    if (!localPassword) {
      throw new Error('No password set')
    }

    const [err] = await walletStore.importWallet(importMnemonic.value, localPassword)
    if (err) {
      throw err
    }
    await walletStore.switchWallet(walletStore.walletId)

    isImportWalletDialogOpen.value = false
    toast({
      title: 'Success',
      description: 'Wallet imported successfully'
    })
    setTimeout(() => {
      router.go(-1)
    }, 300)
    sendAccountsChangedEvent(wallets.value)
  } catch (error: any) {
    toast({
      variant: 'destructive',
      title: 'Error',
      description: error.message
    })
  } finally {
    isImporting.value = false
  }
}


const showEditNameDialog = (wallet: WalletData) => {
  editingWallet.value = wallet
  editingName.value = wallet.name
  isEditNameDialogOpen.value = true
}

const saveWalletName = async () => {
  if (!editingWallet.value || !editingName.value || isEditingName.value) return

  try {
    isEditingName.value = true
    // TODO: Implement actual name saving logic
    editingWallet.value.name = editingName.value

    toast({
      title: 'Success',
      description: 'Wallet name updated successfully'
    })
    isEditNameDialogOpen.value = false
  } catch (error: any) {
    toast({
      variant: 'destructive',
      title: 'Error',
      description: error.message || 'Failed to update Wallet name'
    })
  } finally {
    isEditingName.value = false
  }
}

const showAvatarDialog = (wallet: WalletData) => {
  editingWallet.value = wallet
  isAvatarDialogOpen.value = true
}

const selectAvatar = async (imageUrl: string) => {
  if (!editingWallet.value) return

  try {
    // TODO: Implement actual avatar saving logic
    editingWallet.value.avatar = imageUrl

    toast({
      title: 'Success',
      description: 'Avatar updated successfully'
    })
    isAvatarDialogOpen.value = false
  } catch (error: any) {
    toast({
      variant: 'destructive',
      title: 'Error',
      description: error.message || 'Failed to update avatar'
    })
  }
}
</script>

<style scoped>
.h-screen {
  height: 100vh;
  height: 100dvh;
}
</style>
<style scoped>
.h-screen {
  height: 100vh;
  height: 100dvh;
}

/* 自定义滚动条样式 */
.overflow-y-auto {
  scrollbar-width: thin;
  scrollbar-color: rgba(255, 255, 255, 0.1) transparent;
}

/* Webkit browsers */
.overflow-y-auto::-webkit-scrollbar {
  width: 6px;
}

.overflow-y-auto::-webkit-scrollbar-track {
  background: transparent;
}

.overflow-y-auto::-webkit-scrollbar-thumb {
  background-color: rgba(255, 255, 255, 0.1);
  border-radius: 3px;
}

.overflow-y-auto::-webkit-scrollbar-thumb:hover {
  background-color: rgba(255, 255, 255, 0.2);
}
</style>
