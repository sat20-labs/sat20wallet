import wasmConfig from '@/config/wasm'
import { walletError } from '@/types/error'
import service from '@/lib/service'
import { walletStorage } from '@/lib/walletStorage'
import { createPopup } from '@/utils/popup'
import { Message } from '@/types/message'

export default defineBackground(async () => {
  console.log('Hello background!', { id: browser.runtime.id })
  const approveMap = new Map<
    string,
    { windowId: number | undefined; eventData: any }
  >()
  let createdWindowId: any | null = null
  const portMap: any = {}
  const popupListener = async (message: any) => {
    const eventData = message
    const { action, data, metadata = {} } = eventData
    const { windowId, from, messageId } = metadata
    console.log('Background 收到 popup 消息:', eventData)

    if (from === Message.MessageFrom.POPUP) {
      if (action === Message.MessageAction.GET_APPROVE_DATA) {
        const approveData = approveMap.get(windowId.toString())
        if (approveData) {
          portMap.popup.postMessage({
            metadata: {
              from: Message.MessageFrom.BACKGROUND,
              to: Message.MessageTo.POPUP,
              windowId: windowId,
              messageId,
            },
            action: Message.MessageAction.GET_APPROVE_DATA_RESPONSE,
            data: approveData.eventData,
          })
        }
      } else if (action === Message.MessageAction.APPROVE_RESPONSE) {
        console.log('Background 收到 Approve Response:', eventData)
        eventData.metadata.from = Message.MessageFrom.BACKGROUND
        eventData.metadata.to = Message.MessageTo.INJECTED
        await portMap.content.postMessage(eventData)
        // portMap.popup.disconnect()
        // approveMap.delete(windowId.toString())
        // browser.windows.remove(windowId)
      } else if (action === Message.MessageAction.REJECT_RESPONSE) {
        console.log('Background 收到 Reject Response:', eventData)
        eventData.metadata.from = Message.MessageFrom.BACKGROUND
        eventData.metadata.to = Message.MessageTo.INJECTED
        portMap.content.postMessage({
          ...eventData,
          data: null,
          error: walletError.userReject,
        })
        approveMap.delete(windowId.toString())
        portMap.popup.disconnect()
        browser.windows.remove(windowId)
      }
    }
  }
  browser.runtime.onConnect.addListener(async (port) => {
    console.log(`来自 Tab 的连接: ${port.name}`)
    if (port.name === Message.Port.BG_POPUP) {
      portMap.popup = port
      console.log(portMap.popup)
      port.postMessage({
        type: 'CONNECTION_READY',
      })
      const currWin = await chrome.windows.getCurrent()
      if (currWin?.id) {
        const approveData = approveMap.get(currWin.id.toString())
        if (approveData) {
          portMap.popup.onDisconnect.addListener(() => {
            portMap.content.postMessage({
              ...approveData.eventData,
              data: null,
              error: walletError.userReject,
            })
          })
        }
      }

      console.log('Popup 连接 Background 成功')
      portMap.popup.onMessage.addListener(popupListener)
    } else if (port.name === Message.Port.CONTENT_BG) {
      portMap.content = port
      portMap.content.onMessage.addListener(async (event: any) => {
        console.log('BACKGROUND 收到 CONTENT 消息:', event)
        await walletStorage.initializeState()
        const eventData = event
        const { action, type } = eventData
        eventData.metadata.from = Message.MessageFrom.BACKGROUND
        eventData.metadata.to = Message.MessageTo.INJECTED
        const checkWallet = async () => {
          const hasWallet = await service.getHasWallet()
          console.log(await storage.getItem('local:wallet_hasWallet'))
          if (!hasWallet) {
            portMap.content.postMessage({
              ...eventData,
              data: null,
              error: walletError.noWallet,
            })
          }
          return hasWallet
        }
        let resData
        if (type === Message.MessageType.REQUEST) {
          const hasWallet = await checkWallet()
          if (hasWallet) {
            switch (action) {
              case Message.MessageAction.GET_ACCOUNTS:
                resData = await service.getAccounts()
                break
              case Message.MessageAction.GET_PUBLIC_KEY:
                resData = await service.getPublicKey()
                break
              case Message.MessageAction.GET_NETWORK:
                resData = await service.getNetwork()
                break
              case Message.MessageAction.GET_PUBLIC_KEY:
                resData = await service.getPublicKey()
                break
              case Message.MessageAction.GET_BALANCE:
                resData = await service.getBalance()
                break
              case Message.MessageAction.PUSH_TX:
                resData = await service.pushTx(eventData.data)
                break
              case Message.MessageAction.PUSH_PSBT:
                resData = await service.pushPsbt(eventData.data)
                break
            }
            portMap.content.postMessage({
              ...eventData,
              data: resData,
            })
          }
        } else if (type === Message.MessageType.APPROVE) {
          const hasWallet = await checkWallet()
          if (hasWallet) {
            // Close existing windows for the same origin
            const { origin } = eventData.metadata
            for (const [windowId, data] of approveMap.entries()) {
              if (data.eventData.metadata.origin === origin) {
                try {
                  await browser.windows.remove(parseInt(windowId))
                  approveMap.delete(windowId)
                } catch (error) {}
              }
            }

            const newWindow = await createPopup(
              browser.runtime.getURL(`/popup.html#/wallet/approve`)
            )
            createdWindowId = newWindow?.id
            console.log('创建窗口:', newWindow)
            approveMap.set(createdWindowId.toString(), {
              windowId: createdWindowId,
              eventData: event,
            })
            console.log('approveMap:', approveMap)
          }
        }
      })
    }
  })
})
