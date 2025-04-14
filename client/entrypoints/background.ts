import { walletError } from '@/types/error'
import service from '@/lib/service'
import { storage } from 'wxt/storage'
import { walletStorage } from '@/lib/walletStorage'
import { createPopup } from '@/utils/popup'
import { Message } from '@/types/message'
import { Buffer as Buffer3 } from 'buffer'
import { isOriginAuthorized } from '@/lib/authorized-origins'
import { browser } from 'wxt/browser'
globalThis.Buffer = Buffer3
import wasmConfig from '@/config/wasm'

// Keeps the service worker alive in Manifest V3.
function listenToKeepAliveChannel() {
  chrome.runtime.onConnect.addListener((newPort) => {
    if (newPort.name !== 'SUIET_KEEP_ALIVE') return
    newPort.onMessage.addListener((msg) => {
      if (msg.type !== 'KEEP_ALIVE') return
      newPort.postMessage({ type: 'KEEP_ALIVE', payload: 'PONG' })
    })
  })
}

export default defineBackground(() => {
  console.log('Background service worker started.')
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
  loadWalletWasm().then(() => {
    // Stores pending approval requests, keyed by window ID.
    const approveMap = new Map<
      string,
      { windowId: number | undefined; eventData: any }
    >()

    listenToKeepAliveChannel()
    let createdWindowId: any | null = null
    // Holds references to connected ports (content script, popup).
    const portMap: any = {}


    const popupListener = async (message: any) => {
      const eventData = message

      const { action, data, metadata = {} } = eventData
      const { windowId, from, messageId } = metadata

      if (from === Message.MessageFrom.POPUP) {
        if (action === Message.MessageAction.APPROVE_RESPONSE) {
          eventData.metadata.from = Message.MessageFrom.BACKGROUND
          eventData.metadata.to = Message.MessageTo.INJECTED
          if (portMap.content) {
            await portMap.content.postMessage(eventData)
          } else {
            console.error(
              'Content script port not available for APPROVE_RESPONSE'
            )
          }
          approveMap.delete(windowId.toString())
          browser.windows.remove(windowId)
        } else if (action === Message.MessageAction.REJECT_RESPONSE) {
          eventData.metadata.from = Message.MessageFrom.BACKGROUND
          eventData.metadata.to = Message.MessageTo.INJECTED
          if (portMap.content) {
            portMap.content.postMessage({
              ...eventData,
              data: null,
              error: walletError.userReject,
            })
          } else {
            console.error(
              'Content script port not available for REJECT_RESPONSE'
            )
          }

          approveMap.delete(windowId.toString())
          browser.windows.remove(windowId)
        }
      }
    }

    // Handles messages sent via browser.runtime.sendMessage, specifically for popups retrieving approval data.
    browser.runtime.onMessage.addListener(
      (message: any, sender, sendResponse) => {
        const { type, action, metadata } = message

        // Respond to requests from the popup to get the data needed for an approval prompt.
        if (
          type === Message.MessageType.REQUEST &&
          action === Message.MessageAction.GET_APPROVE_DATA &&
          metadata?.windowId
        ) {
          const { windowId } = metadata
          const approveData = approveMap.get(windowId.toString())

          if (approveData) {
            sendResponse({
              action: Message.MessageAction.GET_APPROVE_DATA_RESPONSE,
              data: approveData.eventData,
            })
          }
          // Return true to indicate asynchronous response.
          return true
        }
      }
    )

    browser.runtime.onConnect.addListener(async (port) => {
      if (port.name === Message.Port.BG_POPUP) {
        portMap.popup = port
        // Clean up and reject the request if the popup window is closed by the user.
        const windowIdMatch = port.sender?.tab?.windowId
        if (windowIdMatch) {
          const approveData = approveMap.get(windowIdMatch.toString())
          if (approveData && portMap.content) {
            port.onDisconnect.addListener(() => {
              console.log(
                `Popup window ${windowIdMatch} disconnected or closed. Rejecting associated request.`
              )
              // Check if the request still exists in the map before rejecting.
              if (approveMap.has(windowIdMatch.toString())) {
                portMap.content.postMessage({
                  ...approveData.eventData,
                  metadata: {
                    ...approveData.eventData.metadata,
                    from: Message.MessageFrom.BACKGROUND,
                    to: Message.MessageTo.INJECTED,
                  },
                  data: null,
                  error: walletError.userReject, // User closing the window is treated as rejection.
                })
                approveMap.delete(windowIdMatch.toString())
              }
            })
          }
        } else {
          console.warn(
            'Could not determine windowId for the connecting popup port.'
          )
        }
        portMap.popup.onMessage.addListener(popupListener)
      } else if (port.name === Message.Port.CONTENT_BG) {
        portMap.content = port
        portMap.content.onMessage.addListener(async (event: any) => {
          await walletStorage.initializeState()
          const eventData = event
          const { action, type } = eventData
          const { origin, messageId } = eventData.metadata

          eventData.metadata.from = Message.MessageFrom.BACKGROUND
          eventData.metadata.to = Message.MessageTo.INJECTED

          // List of methods that require the origin to be authorized.
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
            Message.MessageAction.FINALIZE_SELL_ORDER,
            Message.MessageAction.ADD_INPUTS_TO_PSBT,
            Message.MessageAction.ADD_OUTPUTS_TO_PSBT,
            Message.MessageAction.EXTRACT_TX_FROM_PSBT,
            Message.MessageAction.SPLIT_ASSET,
          ]

          const checkWallet = async () => {
            const hasWallet = await service.getHasWallet()
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

          // Verify origin authorization for specific methods.
          if (METHODS_REQUIRING_AUTHORIZATION.includes(action)) {
            const authorized = await isOriginAuthorized(origin)
            if (!authorized) {
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
                case Message.MessageAction.EXTRACT_TX_FROM_PSBT:
                  const [extractErr, extractRes] =
                    await service.extractTxFromPsbt(
                      eventData.data.psbtHex,
                      eventData.data.chain
                    )
                  if (extractErr || !extractRes) {
                    errData = {
                      code: -22,
                      message: extractErr?.message || '提取交易失败',
                    }
                  } else {
                    resData = extractRes.tx
                  }
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
                case Message.MessageAction.FINALIZE_SELL_ORDER:
                  const [finalizeErr, finalizeRes] =
                    await service.finalizeSellOrder(
                      eventData.data.psbtHex,
                      eventData.data.utxos,
                      eventData.data.buyerAddress,
                      eventData.data.serverAddress,
                      eventData.data.network,
                      eventData.data.serviceFee,
                      eventData.data.networkFee
                    )
                  if (finalizeErr) {
                    errData = {
                      code: -22,
                      message: finalizeErr.message,
                    }
                  } else {
                    resData = finalizeRes
                  }
                  break

                case Message.MessageAction.ADD_INPUTS_TO_PSBT:
                  const [inputsErr, inputsRes] = await service.addInputsToPsbt(
                    eventData.data.psbtHex,
                    eventData.data.utxos
                  )
                  if (inputsErr) {
                    errData = {
                      code: -22,
                      message: inputsErr.message,
                    }
                  } else {
                    resData = inputsRes
                  }
                  break

                case Message.MessageAction.ADD_OUTPUTS_TO_PSBT:
                  const [outputsErr, outputsRes] =
                    await service.addOutputsToPsbt(
                      eventData.data.psbtHex,
                      eventData.data.utxos
                    )
                  if (outputsErr) {
                    errData = {
                      code: -22,
                      message: outputsErr.message,
                    }
                  } else {
                    resData = outputsRes
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
              // Close existing approval windows for the same origin before opening a new one.
              const { origin } = eventData.metadata
              for (const [windowId, data] of approveMap.entries()) {
                if (data.eventData.metadata.origin === origin) {
                  try {
                    await browser.windows.remove(parseInt(windowId))
                    approveMap.delete(windowId)
                  } catch (error) {
                    // Ignore errors if the window is already closed.
                  }
                }
              }

              const newWindow = await createPopup(
                browser.runtime.getURL(`/popup.html#/wallet/approve`)
              )
              createdWindowId = newWindow?.id
              if (createdWindowId) {
                // Store the approval request data associated with the new window ID.
                approveMap.set(createdWindowId.toString(), {
                  windowId: createdWindowId,
                  eventData: event, // Store original event data
                })
              } else {
                console.error('Failed to create approval window.')
                // Notify content script if window creation failed.
                portMap.content.postMessage({
                  ...eventData,
                  error: {
                    code: -1,
                    message: 'Failed to create approval popup',
                  },
                  data: null,
                })
              }
            }
          }
        })
      }
    })
    console.log('Background service worker ready.')
  })
})
