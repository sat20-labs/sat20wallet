import { ref, shallowRef, onBeforeMount, onMounted, onBeforeUnmount } from 'vue'
import { Message } from '@/types/message'
// import Port from '@/lib/message/Port'

export const useApprove = () => {
  const approveData = ref<any>(null)
  // const portRef = shallowRef<InstanceType<typeof Port> | null>(null)

  const approve = async (res: any) => {
    // No-op in Capacitor
  }

  const reject = async () => {
    // No-op in Capacitor
  }

  onBeforeMount(() => {
    // portRef.value = new Port({ name: Message.Port.BG_POPUP })
  })

  onMounted(async () => {
    // No-op in Capacitor
  })

  onBeforeUnmount(() => {
    // portRef.value?.disconnect()
    // portRef.value = null
  })

  return { approveData, approve, reject }
}