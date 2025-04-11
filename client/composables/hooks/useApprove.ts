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

    console.log('当前 Popup 窗口:', currWin)

    // portRef.value.onMessage.addListener(async (message: any) => {
    //   const { action, data, metadata } = message
    //   const { from } = metadata
    //   console.log('Popup 收到 Background Port 消息:', message)
    // })

    try {
      console.log('发送 GET_APPROVE_DATA 消息')
      const response = await browser.runtime.sendMessage({
        type: Message.MessageType.REQUEST,
        action: Message.MessageAction.GET_APPROVE_DATA,
        metadata: {
          from: Message.MessageFrom.POPUP,
          to: Message.MessageTo.BACKGROUND,
          windowId: currWin.id,
        },
      })
      console.log('Popup 收到 Background sendMessage 响应:', response)
      if (response && response.action === Message.MessageAction.GET_APPROVE_DATA_RESPONSE) {
        approveData.value = response.data
      } else {
        console.error('获取批准数据失败或响应格式不正确:', response)
      }
    } catch (error) {
      console.error('发送 GET_APPROVE_DATA 消息时出错:', error)
    }
  })

  onBeforeUnmount(() => {
    portRef.value?.disconnect()
    portRef.value = null
  })
  return { approveData, approve, reject }
}
