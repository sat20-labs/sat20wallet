import { Message } from '@/types/message'

export const useApprove = () => {
  const approveData = ref<any>(null)
  const portRef = shallowRef<any>(null)
  const approve = async (res: any) => {
    const currWin = await browser.windows.getCurrent()
    console.log(approveData.value)

    await portRef.value.postMessage({
      type: Message.MessageType.APPROVE,
      action: Message.MessageAction.APPROVE_RESPONSE,
      metadata: {
        ...approveData.value.metadata,
        from: Message.MessageFrom.POPUP,
        to: Message.MessageTo.BACKGROUND,
        windowId: currWin.id,
      },
      data: res,
    })
  }
  const reject = async () => {
    const currWin = await browser.windows.getCurrent()
    await portRef.value.postMessage({
      type: Message.MessageType.APPROVE,
      action: Message.MessageAction.REJECT_RESPONSE,
      metadata: {
        ...approveData.value.metadata,
        from: Message.MessageFrom.POPUP,
        to: Message.MessageTo.BACKGROUND,
        windowId: currWin.id,
      },
    })
  }
  onBeforeMount(() => {
    portRef.value = browser.runtime.connect({ name: Message.Port.BG_POPUP })
  })
  onMounted(async () => {
    if (!portRef.value) return
    const currWin = await browser.windows.getCurrent()
    const connectionReady = new Promise((resolve) => {
      portRef.value.onMessage.addListener(function connectionListener(
        msg: any
      ) {
        if (msg.type === 'CONNECTION_READY') {
          portRef.value.onMessage.removeListener(connectionListener)
          resolve(true)
        }
      })
    })
    console.log('当前 Popup 窗口:', currWin)

    await connectionReady
    portRef.value.onMessage.addListener(async (message: any) => {
      const { action, data, metadata } = message
      const { from } = metadata
      console.log('Popup 收到 Background 消息:', message)

      if (from === Message.MessageFrom.BACKGROUND) {
        if (action === Message.MessageAction.GET_APPROVE_DATA_RESPONSE) {
          approveData.value = data
        }
      }
    })
    portRef.value.postMessage({
      type: Message.MessageType.REQUEST,
      action: Message.MessageAction.GET_APPROVE_DATA,
      metadata: {
        from: Message.MessageFrom.POPUP,
        to: Message.MessageTo.BACKGROUND,
        windowId: currWin.id,
      },
    })
  })

  onBeforeUnmount(() => {
    // portRef.value?.disconnect()
    portRef.value = null
  })
  return { approveData, approve, reject }
}
