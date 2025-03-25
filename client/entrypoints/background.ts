import { walletError } from '@/types/error'
import service from '@/lib/service'
import { storage } from 'wxt/storage'
import { walletStorage } from '@/lib/walletStorage'
import { createPopup } from '@/utils/popup'
import { Message } from '@/types/message'
import { Buffer as Buffer3 } from 'buffer'
import { isOriginAuthorized } from '@/lib/authorized-origins'
globalThis.Buffer = Buffer3
const Buffer = Buffer3
import wasmConfig from '@/config/wasm'

export default defineBackground(async () => {
  const loadWalletWasm = async () => {
    importScripts('/wasm/wasm_exec.js')
    const go = new Go()
    const wasmPath = browser.runtime.getURL('/wasm/sat20wallet.wasm')
    const response = await fetch(wasmPath)
    const wasmBinary = await response.arrayBuffer()
    const wasmModule = await WebAssembly.instantiate(
      wasmBinary,
      go.importObject
    )
    go.run(wasmModule.instance)
    await (globalThis as any).sat20wallet_wasm.init(
      wasmConfig.config,
      wasmConfig.logLevel
    )
  }
  await loadWalletWasm()
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
        portMap.popup.disconnect()
        approveMap.delete(windowId.toString())
        browser.windows.remove(windowId)
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
      const currWin = await browser.windows.getCurrent()
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
        const { origin } = eventData.metadata

        eventData.metadata.from = Message.MessageFrom.BACKGROUND
        eventData.metadata.to = Message.MessageTo.INJECTED

        // 定义需要验证 origin 的方法列表
        const METHODS_REQUIRING_AUTHORIZATION = [
          Message.MessageAction.GET_ACCOUNTS,
          Message.MessageAction.GET_PUBLIC_KEY,
          Message.MessageAction.GET_BALANCE,
          Message.MessageAction.GET_NETWORK,
          Message.MessageAction.BUILD_BATCH_SELL_ORDER,
          Message.MessageAction.SPLIT_BATCH_SIGNED_PSBT,
          Message.MessageAction.SEND_BITCOIN,
          Message.MessageAction.SIGN_MESSAGE,
          Message.MessageAction.SIGN_PSBT,
          Message.MessageAction.SIGN_PSBTS,
          Message.MessageAction.PUSH_TX,
          Message.MessageAction.PUSH_PSBT,
          Message.MessageAction.GET_INSCRIPTIONS,
          Message.MessageAction.SEND_INSCRIPTION,
          Message.MessageAction.SWITCH_NETWORK,
        ]

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
        let resData = null
        let errData = null
        // 验证 origin 是否已授权（除了 REQUEST_ACCOUNTS 外的所有请求）

        if (METHODS_REQUIRING_AUTHORIZATION.includes(action)) {
          const authorized = await isOriginAuthorized(origin)
          if (!authorized) {
            console.log(`未授权的来源: ${origin}`)
            portMap.content.postMessage({
              ...eventData,
              data: null,
              error: {
                code: -32603,
                message: '未授权的来源，请先调用 REQUEST_ACCOUNTS 方法',
              },
            })
            return
          }
        }
        if (type === Message.MessageType.REQUEST) {
          const hasWallet = await checkWallet()
          if (hasWallet) {
            switch (action) {
              case Message.MessageAction.BUILD_BATCH_SELL_ORDER:
                resData = await service.buildBatchSellOrder(
                  eventData.data.utxos,
                  eventData.data.address,
                  eventData.data.network
                )
                break
              case Message.MessageAction.SPLIT_BATCH_SIGNED_PSBT:
                resData = await service.splitBatchSignedPsbt(
                  eventData.data.signedHex,
                  eventData.data.network
                )
                break
                  
              case Message.MessageAction.GET_ACCOUNTS:
                resData = await service.getAccounts()
                break
              case Message.MessageAction.GET_PUBLIC_KEY:
                resData = await service.getPublicKey()
                break
              case Message.MessageAction.GET_NETWORK:
                resData = await service.getNetwork()
                break
              case Message.MessageAction.GET_BALANCE:
                resData = await service.getBalance()
                break
              case Message.MessageAction.PUSH_TX:
                resData = await service.pushTx(eventData.data.rawtx)
                break
              case Message.MessageAction.PUSH_PSBT:
                const [err, res] = await service.pushPsbt(
                  eventData.data.psbtHex
                )
                if (err) {
                  errData = {
                    code: -22,
                    message: err.message,
                  }
                } else {
                  resData = res
                }
                break
            }
            const responseData = {
              ...eventData,
              data: resData,
            }
            if (errData) {
              responseData.error = errData
            }
            portMap.content.postMessage(responseData)
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
