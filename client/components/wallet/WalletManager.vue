<template>
  <div class="layout-container h-screen flex flex-col">
    <!-- Header -->
    <header class="flex-none z-40 flex items-center justify-between p-4 border-b">
      <div class="flex items-center gap-2">
        <Button variant="ghost" size="icon" @click="router.back()">
          <Icon icon="lucide:arrow-left" class="w-5 h-5" />
        </Button>
        <h1 class="text-lg font-semibold">Wallet Manager</h1>
      </div>
    </header>

    <!-- Scrollable Content -->
    <main class="flex-1 overflow-y-auto">
      <div class="container max-w-2xl mx-auto p-4 space-y-6">
        <div class="space-y-4">
          <div class="text-sm font-medium text-muted-foreground">
            Recovery Phase 1
          </div>
          
          <!-- Wallet List -->
          <div class="space-y-2">
            <div
              v-for="wallet in wallets"
              :key="wallet.id"
              class="flex items-center justify-between p-3 rounded-lg border hover:bg-accent/50 transition-colors"
              :class="{ 'border-primary/50': Number(wallet.id) === currentWalletIndex }"
            >
              <div class="flex items-center gap-3">
                <Button
                  variant="ghost"
                  size="icon"
                  class="w-10 h-10 p-0 rounded-full overflow-hidden"
                  @click="showAvatarDialog(wallet)"
                  v-if="Number(wallet.id) === currentWalletIndex"
                >
                  <img 
                    v-if="wallet.avatar" 
                    :src="wallet.avatar" 
                    :alt="wallet.name"
                    class="w-full h-full object-cover"
                  />
                  <div 
                    v-else 
                    class="w-full h-full flex items-center justify-center bg-muted/80"
                  >
                    <Icon icon="lucide:wallet" class="w-5 h-5 text-white/60" />
                  </div>
                </Button>
                <div 
                  v-else
                  class="w-10 h-10 rounded-full overflow-hidden"
                >
                  <img 
                    v-if="wallet.avatar" 
                    :src="wallet.avatar" 
                    :alt="wallet.name"
                    class="w-full h-full object-cover"
                  />
                  <div 
                    v-else 
                    class="w-full h-full flex items-center justify-center bg-muted/80"
                  >
                    <Icon icon="lucide:wallet" class="w-5 h-5 text-white/60" />
                  </div>
                </div>
                <div>
                  <div class="font-medium flex items-center gap-2 text-white/60">
                    {{ wallet.name }}
                    <Button
                      v-if="Number(wallet.id) === currentWalletIndex"
                      variant="ghost"
                      size="icon"
                      class="h-2 w-2"
                      @click="showEditNameDialog(wallet)"
                    >
                      <Icon icon="lucide:pencil" class="w-2 h-2" />
                    </Button>
                  </div>
                  <div class="text-sm text-muted-foreground">{{ formatBalance(wallet.balance) }}</div>
                </div>
              </div>
              <div class="flex items-center gap-2">
                <Button
                  v-if="Number(wallet.id) !== currentWalletIndex"
                  variant="outline"
                  size="sm"
                  @click="selectWallet(wallet)"
                >
                  Switch
                </Button>
                <Button
                  v-else
                  variant="outline"
                  size="sm"
                  disabled
                >
                  Current
                </Button>
                <Button
                  v-if="Number(wallet.id) !== currentWalletIndex"
                  variant="ghost"
                  size="icon"
                  class="text-destructive hover:text-destructive"
                  @click="confirmDeleteWallet(wallet)"
                >
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
          <Button
            class="flex-1 gap-2 h-11 flex items-center"
            variant="default"
            @click="showCreateWalletDialog"
          >
            <Icon icon="lucide:plus-circle" class="w-6 h-6 flex-shrink-0" />
            Create Wallet
          </Button>
          <Button
            class="flex-1 gap-2 h-11"
            variant="secondary"
            @click="showImportWalletDialog"
          >
            <Icon icon="lucide:import" class="w-6 h-6 flex-shrink-0" />
            Import Wallet
          </Button>
        </div>
      </div>
    </footer>

    <!-- Edit Name Dialog -->
    <Dialog :open="isEditNameDialogOpen" @update:open="isEditNameDialogOpen = $event">
      <DialogContent class="sm:max-w-[425px]">
        <DialogHeader>
          <DialogTitle>EDIT WALLET NAME</DialogTitle>
          <DialogDescription>
            <hr class="mb-6 mt-1 border-t-1 border-accent">
            Change the name of your wallet
          </DialogDescription>
        </DialogHeader>
        <div class="space-y-4">
          <div class="space-y-2">
            <Label for="walletName">Wallet Name</Label>
            <Input
              id="walletName"
              v-model="editingName"
              placeholder="Enter wallet name"
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="secondary" @click="isEditNameDialogOpen = false" class="h-11 mt-2">
            Cancel
          </Button>
          <Button 
            :disabled="isEditingName"
            @click="saveWalletName"
            class="h-11 mt-2"
          >
            <Icon v-if="isEditingName" icon="lucide:loader-2" class="w-4 h-4 mr-2 animate-spin" />
            {{ isEditingName ? 'Saving...' : 'Save' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- Avatar Dialog -->
    <Dialog :open="isAvatarDialogOpen" @update:open="isAvatarDialogOpen = $event">
      <DialogContent class="sm:max-w-[425px]">
        <DialogHeader>
          <DialogTitle>Change Avatar</DialogTitle>
          <DialogDescription>
            <hr class="mb-6 mt-1 border-t-1 border-accent">
            Choose from your NFTs or BTC domains
          </DialogDescription>
        </DialogHeader>
        <div class="space-y-4">
          <Tabs defaultValue="nfts" class="w-full">
            <TabsList class="grid w-full grid-cols-2">
              <TabsTrigger value="nfts">NFTs</TabsTrigger>
              <TabsTrigger value="domains">BTC Domains</TabsTrigger>
            </TabsList>
            <TabsContent value="nfts">
              <div class="grid grid-cols-3 gap-2">
                <Button
                  v-for="nft in nfts"
                  :key="nft.id"
                  variant="outline"
                  class="aspect-square p-0 relative group"
                  @click="selectAvatar(nft.image)"
                >
                  <img 
                    :src="nft.image" 
                    :alt="nft.name"
                    class="w-full h-full object-cover rounded-lg"
                  />
                  <div class="absolute inset-0 bg-black/50 flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity">
                    <Icon icon="lucide:check" class="w-6 h-6 text-white" />
                  </div>
                </Button>
              </div>
            </TabsContent>
            <TabsContent value="domains">
              <div class="grid grid-cols-3 gap-2">
                <Button
                  v-for="domain in btcDomains"
                  :key="domain.id"
                  variant="outline"
                  class="aspect-square p-0 relative group"
                  @click="selectAvatar(domain.image)"
                >
                  <img 
                    :src="domain.image" 
                    :alt="domain.name"
                    class="w-full h-full object-cover rounded-lg"
                  />
                  <div class="absolute inset-0 bg-black/50 flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity">
                    <Icon icon="lucide:check" class="w-6 h-6 text-white" />
                  </div>
                </Button>
              </div>
            </TabsContent>
          </Tabs>
        </div>
        <DialogFooter>
          <Button variant="secondary" @click="isAvatarDialogOpen = false" class="h-11 mt-2">
            Cancel
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- Create Wallet Dialog -->
    <Dialog :open="isCreateWalletDialogOpen" @update:open="isCreateWalletDialogOpen = $event">
      <DialogContent class="sm:max-w-[425px]">
        <DialogHeader>
          <DialogTitle>CREATE NEW WALLET</DialogTitle>
          <DialogDescription>
            <hr class="mb-6 mt-1 border-t-1 border-accent">
            Set up your new wallet password.
          </DialogDescription>
        </DialogHeader>
        <form @submit.prevent="createWallet" class="space-y-4">
          <div class="space-y-4">
            <div class="space-y-2">
              <Label for="password">Password</Label>
              <Input
                id="password"
                v-model="newWalletPassword"
                type="password"
                placeholder="Enter password"
              />
              <p class="text-xs text-muted-foreground">
                Password must be at least 8 characters with uppercase and number
              </p>
            </div>
            <div class="space-y-2">
              <Label for="confirmPassword">Confirm Password</Label>
              <Input
                id="confirmPassword"
                v-model="newWalletConfirmPassword"
                type="password"
                placeholder="Confirm password"
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="secondary" type="button" @click="isCreateWalletDialogOpen = false" class="h-11 mt-2">
              Cancel
            </Button>
            <Button type="submit" :disabled="isCreating" class="h-11 mt-2">
              <Icon v-if="isCreating" icon="lucide:loader-2" class="w-4 h-4 mr-2 animate-spin" />
              {{ isCreating ? 'Creating...' : 'Create' }}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <!-- Import Wallet Dialog -->
    <Dialog :open="isImportWalletDialogOpen" @update:open="isImportWalletDialogOpen = $event">
      <DialogContent class="sm:max-w-[425px]">
        <DialogHeader>
          <DialogTitle>IMPORT WALLET</DialogTitle>
          <DialogDescription>
            <hr class="mb-6 mt-1 border-t-1 border-accent">
            Import your existing wallet using recovery phrase
          </DialogDescription>
        </DialogHeader>
        <form @submit.prevent="importWallet" class="space-y-4">
          <div class="space-y-4">
            <div class="space-y-2">
              <Label for="mnemonic">RECOVERY PHRASE</Label>
              <Textarea
                id="mnemonic"
                v-model="importMnemonic"
                placeholder="Enter your recovery phrase, words separated by spaces"
                rows="3"
              />
              <p class="text-xs text-muted-foreground">
                Enter your 12 or 24-word recovery phrase in the correct order
              </p>
            </div>
            <div class="space-y-2">
              <Label for="importPassword">New Wallet Password</Label>
              <Input
                id="importPassword"
                v-model="importPassword"
                type="password"
                placeholder="Enter password"
              />
            </div>
            <div class="space-y-2">
              <Label for="importConfirmPassword">Confirm Password</Label>
              <Input
                id="importConfirmPassword"
                v-model="importConfirmPassword"
                type="password"
                placeholder="Confirm password"
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="secondary" type="button" @click="isImportWalletDialogOpen = false" class="h-11 mt-2">
              Cancel
            </Button>
            <Button type="submit" :disabled="isImporting" class="h-11 mt-2">
              <Icon v-if="isImporting" icon="lucide:loader-2" class="w-4 h-4 mr-2 animate-spin" />
              {{ isImporting ? 'Importing...' : 'Import' }}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <!-- Recovery Phrase Dialog -->
    <Dialog :open="isShowMnemonicDialogOpen" @update:open="isShowMnemonicDialogOpen = $event">
      <DialogContent class="sm:max-w-[425px]">
        <DialogHeader>
          <DialogTitle>SAVE YOUR RECOVERY PHRASE</DialogTitle>
          <DialogDescription>
            <hr class="mb-6 mt-1 border-t-1 border-accent">
            Never share your recovery phrase. Store it securely offline.
          </DialogDescription>
        </DialogHeader>
        <div class="space-y-4">
          <Alert variant="destructive">
            <Icon icon="lucide:alert-triangle" class="w-4 h-4" />
            <AlertDescription>
              Never share your recovery phrase. Store it securely offline.
            </AlertDescription>
          </Alert>
          <div class="relative">
            <div class="grid grid-cols-3 gap-2 p-4 bg-muted rounded-lg">
              <div
                v-for="(word, i) in mnemonic.split(' ')"
                :key="i"
                class="flex items-center space-x-2"
              >
                <span class="text-muted-foreground">{{ i + 1 }}.</span>
                <span :class="showMnemonic ? '' : 'blur-sm select-none'">{{ word }}</span>
              </div>
            </div>
            <Button
              variant="ghost"
              size="icon"
              class="absolute top-2 right-2"
              @click="toggleShowMnemonic"
            >
              <Icon v-if="showMnemonic" icon="lucide:eye-off" class="w-4 h-4" />
              <Icon v-else icon="lucide:eye" class="w-4 h-4" />
            </Button>
          </div>
          <div class="flex justify-center">
            <Button variant="outline" @click="handleCopyMnemonic" class="w-full gap-2">
              <Icon icon="lucide:copy" class="w-4 h-4" />
              Copy to clipboard
            </Button>
          </div>
        </div>
        <DialogFooter>
          <Button @click="finishCreation">I've saved my recovery phrase</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- Delete Confirmation Dialog -->
    <Dialog :open="isDeleteDialogOpen" @update:open="isDeleteDialogOpen = $event">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>DELETE WALLET</DialogTitle>
          <DialogDescription>
            <hr class="mb-6 mt-1 border-t-1 border-accent">
            Are you sure you want to delete this wallet? This action cannot be undone.
          </DialogDescription>
        </DialogHeader>
        <div class="space-y-4">
          <Alert variant="destructive">
            <Icon icon="lucide:alert-triangle" class="w-4 h-4" />
            <AlertDescription>
              Make sure you have backed up your recovery phrase before deleting this wallet.
            </AlertDescription>
          </Alert>
        </div>
        <DialogFooter>
          <Button variant="secondary" @click="isDeleteDialogOpen = false" class="h-11 mt-2">
            Cancel
          </Button>
          <Button 
            variant="default" 
            :disabled="isDeleting"
            @click="deleteWallet"
            class="h-11 mt-2"
          >
            <Icon v-if="isDeleting" icon="lucide:loader-2" class="w-4 h-4 mr-2 animate-spin" />
            {{ isDeleting ? 'Deleting...' : 'Delete' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
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
import { hashPassword } from '@/utils/crypto'

const router = useRouter()
const walletStore = useWalletStore()
const { toast } = useToast()

// State
const isCreateWalletDialogOpen = ref(false)
const isImportWalletDialogOpen = ref(false)
const isShowMnemonicDialogOpen = ref(false)
const isDeleteDialogOpen = ref(false)
const isEditNameDialogOpen = ref(false)
const isAvatarDialogOpen = ref(false)
const isCreating = ref(false)
const isImporting = ref(false)
const isDeleting = ref(false)
const isEditingName = ref(false)
const showMnemonic = ref(false)

const newWalletPassword = ref('')
const newWalletConfirmPassword = ref('')
const importMnemonic = ref('')
const importPassword = ref('')
const importConfirmPassword = ref('')
const mnemonic = ref('')
const walletToDelete = ref<any>(null)
const editingWallet = ref<any>(null)
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
const currentWalletIndex = computed(() => walletStore.accountIndex || 0)

// 模拟钱包列表
const wallets = computed(() => {
  const totalWallets = 8 // 这里可以根据实际情况调整
  return Array.from({ length: totalWallets }, (_, index) => ({
    id: index.toString(),
    name: `Wallet ${index + 1}`,
    balance: index === currentWalletIndex.value ? 854.54 : 0, // 模拟余额
    avatar: index === 0 ? 'https://picsum.photos/200' : null,
  }))
})

// Methods
const formatBalance = (balance: number) => {
  return `$${balance.toFixed(2)}`
}

const selectWallet = async (wallet: any) => {
  try {
    await walletStore.setAccountIndex(Number(wallet.id))
    router.back()
  } catch (error) {
    console.error('Failed to switch wallet:', error)
    toast({
      variant: 'destructive',
      title: 'Error',
      description: 'Failed to switch wallet'
    })
  }
}

const confirmDeleteWallet = (wallet: any) => {
  walletToDelete.value = wallet
  isDeleteDialogOpen.value = true
}

const deleteWallet = async () => {
  if (!walletToDelete.value || isDeleting.value) return

  try {
    isDeleting.value = true
    const [err] = await walletStore.deleteWallet()

    if (err) {
      throw err
    }

    toast({
      title: 'Success',
      description: 'Wallet deleted successfully'
    })
    isDeleteDialogOpen.value = false
    walletToDelete.value = null
    router.back()
  } catch (error: any) {
    toast({
      variant: 'destructive',
      title: 'Error',
      description: error.message || 'Failed to delete Wallet'
    })
  } finally {
    isDeleting.value = false
  }
}

const showCreateWalletDialog = () => {
  newWalletPassword.value = ''
  newWalletConfirmPassword.value = ''
  isCreateWalletDialogOpen.value = true
}

const showImportWalletDialog = () => {
  importMnemonic.value = ''
  importPassword.value = ''
  importConfirmPassword.value = ''
  isImportWalletDialogOpen.value = true
}

const validatePassword = (password: string, confirmPassword: string) => {
  if (password.length < 8) {
    throw new Error('Password must be at least 8 characters')
  }
  if (!/[A-Z]/.test(password)) {
    throw new Error('Password must contain at least one uppercase letter')
  }
  if (!/\d/.test(password)) {
    throw new Error('Password must contain at least one number')
  }
  if (password !== confirmPassword) {
    throw new Error('Passwords do not match')
  }
}

const createWallet = async () => {
  if (isCreating.value) return
  
  try {
    validatePassword(newWalletPassword.value, newWalletConfirmPassword.value)
    
    isCreating.value = true
    const hashedPassword = await hashPassword(newWalletPassword.value)
    const [err, result] = await walletStore.createWallet(hashedPassword)
    
    if (err || !result) {
      throw err || new Error('Failed to create wallet')
    }

    mnemonic.value = result as string
    isCreateWalletDialogOpen.value = false
    isShowMnemonicDialogOpen.value = true
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
    validatePassword(importPassword.value, importConfirmPassword.value)

    if (!importMnemonic.value) {
      throw new Error('Please enter your recovery phrase')
    }

    isImporting.value = true
    const hashedPassword = await hashPassword(importPassword.value)
    const [err] = await walletStore.importWallet(importMnemonic.value, hashedPassword)

    if (err) {
      throw err
    }

    isImportWalletDialogOpen.value = false
    router.back()
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

const toggleShowMnemonic = () => {
  showMnemonic.value = !showMnemonic.value
}

const handleCopyMnemonic = async () => {
  try {
    await navigator.clipboard.writeText(mnemonic.value)
    toast({
      title: 'Success',
      description: 'Recovery phrase copied to clipboard'
    })
  } catch (error) {
    toast({
      variant: 'destructive',
      title: 'Error',
      description: 'Failed to copy recovery phrase'
    })
  }
}

const finishCreation = () => {
  isShowMnemonicDialogOpen.value = false
  router.back()
}

const showEditNameDialog = (wallet: any) => {
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

const showAvatarDialog = (wallet: any) => {
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
