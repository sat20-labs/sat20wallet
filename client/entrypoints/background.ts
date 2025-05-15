import { walletError } from '@/types/error'
import service from '@/lib/service'
import { storage } from 'wxt/storage'
import { walletStorage } from '@/lib/walletStorage'
import { createPopup } from '@/utils/popup'
import { Message } from '@/types/message'
import { Buffer as Buffer3 } from 'buffer'
import { isOriginAuthorized } from '@/lib/authorized-origins'
import { browser } from 'wxt/browser'
// Import Port type from the polyfill
import type { Runtime } from 'wxt/browser';

globalThis.Buffer = Buffer3
import { getConfig, logLevel } from '@/config/wasm'

// Keeps the service worker alive in Manifest V3.
function listenToKeepAliveChannel() {
  chrome.runtime.onConnect.addListener((newPort) => {
    if (newPort.name !== 'KEEP_ALIVE_INTERVAL') return
    newPort.onMessage.addListener((msg) => {
      if (msg.type !== 'KEEP_ALIVE') return
      newPort.postMessage({ type: 'KEEP_ALIVE', payload: 'PONG' })
    })
  })
}

export default defineBackground(() => {
  console.log('Background service worker started.')

  // 添加消息队列和WASM状态标识
  let isWasmReady = false
  const messageQueue: { port: Runtime.Port; event: any }[] = []

  // 处理队列中的消息
  const processQueuedMessages = async () => {
    console.log(`Processing queued messages, count: ${messageQueue.length}`)
    while (messageQueue.length > 0) {
      const { port, event } = messageQueue.shift()!
      await handleMessage(port, event)
    }
  }

  // 消息处理函数
  const handleMessage = async (port: Runtime.Port, event: any) => {
    const eventData = event
    const { action, type, data } = eventData
    const { origin, messageId } = eventData.metadata

    eventData.metadata.from = Message.MessageFrom.BACKGROUND
    eventData.metadata.to = Message.MessageTo.INJECTED

    try {
      await walletStorage.initializeState()
      const hasWallet = await service.getHasWallet()
      if (!hasWallet) {
        port.postMessage({
          ...eventData,
          data: null,
          error: walletError.noWallet,
        })
        return
      }

      // List of methods that require the origin to be authorized.
      const METHODS_REQUIRING_AUTHORIZATION = [
        Message.MessageAction.GET_ACCOUNTS,
        Message.MessageAction.GET_PUBLIC_KEY,
        Message.MessageAction.GET_BALANCE,
        Message.MessageAction.GET_NETWORK,
        Message.MessageAction.BUILD_BATCH_SELL_ORDER,
        Message.MessageAction.SPLIT_BATCH_SIGNED_PSBT,
        Message.MessageAction.SPLIT_BATCH_SIGNED_PSBT_SATSNET,
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
        Message.MessageAction.EXTRACT_TX_FROM_PSBT_SATSNET,
        Message.MessageAction.SPLIT_ASSET,
        // --- Added Actions ---
        Message.MessageAction.LOCK_UTXO,
        Message.MessageAction.LOCK_UTXO_SATSNET,
        Message.MessageAction.UNLOCK_UTXO,
        Message.MessageAction.UNLOCK_UTXO_SATSNET,
        Message.MessageAction.GET_ALL_LOCKED_UTXO,
        Message.MessageAction.GET_ALL_LOCKED_UTXO_SATSNET,
        Message.MessageAction.LOCK_TO_CHANNEL,
        Message.MessageAction.UNLOCK_FROM_CHANNEL,
        // --- Added UTXO Getter Actions ---
        Message.MessageAction.GET_UTXOS,
        Message.MessageAction.GET_UTXOS_SATSNET,
        Message.MessageAction.GET_UTXOS_WITH_ASSET,
        Message.MessageAction.GET_UTXOS_WITH_ASSET_SATSNET,
        Message.MessageAction.GET_UTXOS_WITH_ASSET_V2,
        Message.MessageAction.GET_UTXOS_WITH_ASSET_V2_SATSNET,
        Message.MessageAction.GET_ASSET_AMOUNT,
        Message.MessageAction.GET_ASSET_AMOUNT_SATSNET,
        Message.MessageAction.MERGE_BATCH_SIGNED_PSBT,
      ]

      // Verify origin authorization for specific methods.
      if (METHODS_REQUIRING_AUTHORIZATION.includes(action)) {
        const authorized = await isOriginAuthorized(origin)
        if (!authorized) {
          port.postMessage({ // Use port directly
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

      let resData = null
      let errData = null

      if (type === Message.MessageType.REQUEST) {
        // Handle REQUEST type messages
        let reqErr: Error | undefined, reqRes: any | undefined; // Define vars for results

        switch (action) {
          case Message.MessageAction.BUILD_BATCH_SELL_ORDER:
            resData = await service.buildBatchSellOrder_SatsNet(
              data.utxos,
              data.address,
              data.network
            )
            break
          case Message.MessageAction.SPLIT_BATCH_SIGNED_PSBT:
            resData = await service.splitBatchSignedPsbt(
              data.signedHex,
              data.network
            )
            break
          case Message.MessageAction.SPLIT_BATCH_SIGNED_PSBT_SATSNET:
            resData = await service.splitBatchSignedPsbt_SatsNet(
              data.signedHex,
              data.network
            )
            break
          case Message.MessageAction.EXTRACT_TX_FROM_PSBT:
            const [extractErr, extractRes] = await service.extractTxFromPsbt(
              data.psbtHex,
              data.chain
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
            resData = await service.pushTx(data.rawtx)
            break
          case Message.MessageAction.PUSH_PSBT:
            const [err, res] = await service.pushPsbt(data.psbtHex)
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
            const [finalizeErr, finalizeRes] = await service.finalizeSellOrder_SatsNet(
              data.psbtHex,
              data.utxos,
              data.buyerAddress,
              data.serverAddress,
              data.network,
              data.serviceFee,
              data.networkFee
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
          case Message.MessageAction.MERGE_BATCH_SIGNED_PSBT:
            resData = await service.mergeBatchSignedPsbt_SatsNet(
              data.psbts,
              data.network
            )
            break
          case Message.MessageAction.ADD_INPUTS_TO_PSBT:
            const [inputsErr, inputsRes] = await service.addInputsToPsbt(
              data.psbtHex,
              data.utxos
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
            const [outputsErr, outputsRes] = await service.addOutputsToPsbt(
              data.psbtHex,
              data.utxos
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

          // --- Added Cases for REQUEST ---
          case Message.MessageAction.GET_ALL_LOCKED_UTXO:
            [reqErr, reqRes] = await service.getAllLockedUtxo(data.address);
            if (reqErr) {
              errData = { code: -30, message: reqErr.message }; // Example error code
            } else {
              resData = reqRes;
            }
            break;
          case Message.MessageAction.GET_ALL_LOCKED_UTXO_SATSNET:
            [reqErr, reqRes] = await service.getAllLockedUtxo_SatsNet(data.address);
            if (reqErr) {
              errData = { code: -31, message: reqErr.message }; // Example error code
            } else {
              resData = reqRes;
            }
            break;
          // --- End Added Cases ---

          // --- Added Cases for UTXO Getters ---
          case Message.MessageAction.GET_UTXOS:
            [reqErr, reqRes] = await service.getUtxos();
            if (reqErr) { errData = { code: -40, message: reqErr.message }; } else { resData = reqRes; }
            break;
          case Message.MessageAction.GET_UTXOS_SATSNET:
            [reqErr, reqRes] = await service.getUtxos_SatsNet();
            if (reqErr) { errData = { code: -41, message: reqErr.message }; } else { resData = reqRes; }
            break;
          case Message.MessageAction.GET_UTXOS_WITH_ASSET:
            [reqErr, reqRes] = await service.getUtxosWithAsset(data.address, data.amt, data.assetName);
            if (reqErr) { errData = { code: -42, message: reqErr.message }; } else { resData = reqRes; }
            break;
          case Message.MessageAction.GET_UTXOS_WITH_ASSET_SATSNET:
            [reqErr, reqRes] = await service.getUtxosWithAsset_SatsNet(data.address, data.amt, data.assetName);
            if (reqErr) { errData = { code: -43, message: reqErr.message }; } else { resData = reqRes; }
            break;
          case Message.MessageAction.GET_UTXOS_WITH_ASSET_V2:
            [reqErr, reqRes] = await service.getUtxosWithAssetV2(data.address, data.amt, data.assetName);
            if (reqErr) { errData = { code: -44, message: reqErr.message }; } else { resData = reqRes; }
            break;
          case Message.MessageAction.GET_UTXOS_WITH_ASSET_V2_SATSNET:
            [reqErr, reqRes] = await service.getUtxosWithAssetV2_SatsNet(data.address, data.amt, data.assetName);
            if (reqErr) { errData = { code: -45, message: reqErr.message }; } else { resData = reqRes; }
            break;
          case Message.MessageAction.GET_ASSET_AMOUNT:
            [reqErr, reqRes] = await service.getAssetAmount(data.address, data.assetName);
            if (reqErr) { errData = { code: -46, message: reqErr.message }; } else { resData = reqRes; }
            break;
          case Message.MessageAction.GET_ASSET_AMOUNT_SATSNET:
            [reqErr, reqRes] = await service.getAssetAmount_SatsNet(data.address, data.assetName);
            if (reqErr) { errData = { code: -47, message: reqErr.message }; } else { resData = reqRes; } // Handle potential error/result
            break;
          case Message.MessageAction.LOCK_UTXO:
            [reqErr, reqRes] = await service.lockUtxo(data.address, data.utxo, data.reason);
            if (reqErr) { errData = { code: -48, message: reqErr.message }; } else { resData = { success: true }; }
            break;

          case Message.MessageAction.UNLOCK_UTXO:
            [reqErr, reqRes] = await service.unlockUtxo(data.address, data.utxo);
            if (reqErr) { errData = { code: -49, message: reqErr.message }; } else { resData = { success: true }; }
            break;
          case Message.MessageAction.LOCK_UTXO_SATSNET:
            [reqErr, reqRes] = await service.lockUtxo_SatsNet(data.address, data.utxo, data.reason);
            if (reqErr) { errData = { code: -48, message: reqErr.message }; } else { resData = { success: true }; }
            break;
          case Message.MessageAction.UNLOCK_UTXO_SATSNET:
            [reqErr, reqRes] = await service.unlockUtxo_SatsNet(data.address, data.utxo);
            if (reqErr) { errData = { code: -49, message: reqErr.message }; } else { resData = { success: true }; }
            break;
          case Message.MessageAction.GET_TICKER_INFO:
            [reqErr, reqRes] = await service.getTickerInfo(data.asset);
            if (reqErr) {
              errData = { code: -50, message: reqErr.message };
            } else {
              resData = reqRes;
            }
            break;
          default:
            console.warn(`Unhandled REQUEST action: ${action}`);
            errData = { code: -32601, message: 'Method not found' }; // Method not found error
            break;
        }
        const responseData = {
          ...eventData,
          data: resData,
        }
        if (errData) {
          responseData.error = errData
        }
        port.postMessage(responseData) // Use port directly

      } else if (type === Message.MessageType.APPROVE) {
        // Handle APPROVE type messages
        const REQUIRES_APPROVAL = [
          Message.MessageAction.REQUEST_ACCOUNTS, // Example existing
          Message.MessageAction.SWITCH_NETWORK,
          Message.MessageAction.SEND_BITCOIN,
          Message.MessageAction.SIGN_MESSAGE,
          Message.MessageAction.SIGN_PSBT,
          Message.MessageAction.SIGN_PSBTS,
          Message.MessageAction.SEND_INSCRIPTION,
          Message.MessageAction.SPLIT_ASSET,
          // --- Added Actions Requiring Approval ---
          Message.MessageAction.LOCK_UTXO,

          Message.MessageAction.UNLOCK_UTXO,
          Message.MessageAction.UNLOCK_UTXO_SATSNET,
          // --- Added Channel Actions ---
          Message.MessageAction.LOCK_TO_CHANNEL,
          Message.MessageAction.UNLOCK_FROM_CHANNEL,
          Message.MessageAction.BATCH_SEND_ASSETS_SATSNET,
        ];

        if (REQUIRES_APPROVAL.includes(action)) {
          // Close existing approval windows for the same origin before opening a new one.
          const windowsToRemove: number[] = [];
          for (const [windowIdStr, data] of approveMap.entries()) {
            if (data.eventData.metadata.origin === origin) {
              windowsToRemove.push(data.windowId);
            }
          }
          for (const winId of windowsToRemove) {
            try {
              await browser.windows.remove(winId);
              approveMap.delete(winId.toString()); // Remove from map after closing window
              console.log(`Closed previous approval window ${winId} for origin ${origin}.`);
            } catch (error) {
              console.warn(`Failed to remove previous window ${winId}, maybe already closed:`, error);
              approveMap.delete(winId.toString()); // Ensure cleanup
            }
          }

          const newWindow = await createPopup(
            browser.runtime.getURL(`/popup.html#/wallet/approve`)
          )

          if (newWindow?.id) {
            createdWindowId = newWindow.id; // Store the numeric ID
            approveMap.set(createdWindowId.toString(), { // Use string key for map
              windowId: createdWindowId, // Store numeric ID in value
              eventData: event, // Store original event data
            });
            console.log(`Approval window ${createdWindowId} created for action ${action} and request stored.`);
          } else {
            console.error('Failed to create approval window.')
            port.postMessage({ // Use port directly
              ...eventData,
              error: {
                code: -1,
                message: 'Failed to create approval popup',
              },
              data: null,
            })
          }
        } else {
          // Handle cases where APPROVE type is received but action doesn't require it
          console.warn(`Received APPROVE message for action ${action} which doesn't require approval.`);
          port.postMessage({ // Use port directly
            ...eventData,
            error: { code: -32600, message: 'Invalid Request: Action does not require approval.' },
            data: null,
          });
        }
      }
    } catch (error: any) { // Add type annotation for error
      console.error('Error handling message:', error)
      port.postMessage({ // Use port directly
        ...eventData,
        data: null,
        error: {
          code: -32603, // Internal error code
          message: error?.message || '处理消息时发生内部错误', // Include error message if available
        },
      })
    }
  }

  const loadStpWasm = async () => {
    const go = new Go()
    const wasmPath = browser.runtime.getURL('/wasm/stpd.wasm')
    const env = walletStorage.getValue('env') || 'test'
    const response = await fetch(wasmPath)
    const wasmBinary = await response.arrayBuffer()
    const wasmModule = await WebAssembly.instantiate(
      wasmBinary,
      go.importObject
    )
    go.run(wasmModule.instance)
    await (globalThis as any).stp_wasm.init(
      getConfig(env),
      logLevel
    )
  }
  const loadWalletWasm = async () => {
    try {
      importScripts('/wasm/wasm_exec.js')
      const go = new Go()
      const env = walletStorage.getValue('env') || 'test'
      const wasmPath = browser.runtime.getURL('/wasm/sat20wallet.wasm')
      const response = await fetch(wasmPath)
      const wasmBinary = await response.arrayBuffer()
      const wasmModule = await WebAssembly.instantiate(
        wasmBinary,
        go.importObject
      )
      go.run(wasmModule.instance)
      await (globalThis as any).sat20wallet_wasm.init(
        getConfig(env),
        logLevel
      )
      await loadStpWasm()

      // WASM加载完成,设置状态并处理队列
      isWasmReady = true
      console.log('WASM loaded successfully, processing queued messages')
      await processQueuedMessages()
    } catch (error) {
      console.error('Failed to load WASM:', error)
      throw error
    }
  }

  // Stores pending approval requests, keyed by window ID string.
  // Value includes the numeric windowId for easier comparison later.
  const approveMap = new Map<
    string,
    { windowId: number; eventData: any } // Ensure windowId is number type
  >()

  listenToKeepAliveChannel()
  let createdWindowId: number | null = null // Ensure createdWindowId is number or null
  // Holds references to connected ports (content script, popup).
  const portMap: {
    content?: Runtime.Port
    popup?: Runtime.Port // Potentially store multiple popups if needed
  } = {}

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
        // Ensure cleanup uses the correct key type (string)
        if (windowId) {
          approveMap.delete(windowId.toString())
          try {
            await browser.windows.remove(windowId)
          } catch (e) {
            console.warn(`Window ${windowId} might already be closed:`, e)
          }
        }
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

        // Ensure cleanup uses the correct key type (string)
        if (windowId) {
          approveMap.delete(windowId.toString())
          try {
            await browser.windows.remove(windowId)
          } catch (e) {
            console.warn(`Window ${windowId} might already be closed:`, e)
          }
        }
      }
    }
  }

  // Handles messages sent via browser.runtime.sendMessage, specifically for popups retrieving approval data.
  browser.runtime.onMessage.addListener(
    async (message: any, sender, sendResponse) => {
      const { type, action, metadata } = message
      if (type === Message.MessageType.EVENT) {
        if (portMap.content) {
          portMap.content.postMessage(message)
        }
        return true
      }
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
        } else {
          console.warn(`GET_APPROVE_DATA requested for unknown windowId: ${windowId}`);
          sendResponse(undefined);
        }
        return true
      } else if (type === Message.MessageType.REQUEST && action === Message.MessageAction.ENV_CHANGED) {
        console.log('ENV_CHANGED', message);
        await (globalThis as any).stp_wasm.release()
        await (globalThis as any).stp_wasm.init(getConfig(message.data.env), logLevel)
        await (globalThis as any).sat20wallet_wasm.release()
        await (globalThis as any).sat20wallet_wasm.init(getConfig(message.data.env), logLevel)
      }
      return undefined;
    }
  )

  browser.runtime.onConnect.addListener(async (port) => {
    console.log('onConnect', port);
    if (port.name === Message.Port.BG_POPUP) {
      portMap.popup = port // Note: This overwrites previous popup port. Consider a map if multiple popups needed.
      // Clean up and reject the request if the popup window is closed by the user.
      console.log('Popup connected:', port)

      const windowIdMatch = port.sender?.tab?.windowId
      if (windowIdMatch) {
        const windowIdStr = windowIdMatch.toString();
        // Check if we have data for this window in our map
        if (approveMap.has(windowIdStr)) {
          const approveData = approveMap.get(windowIdStr)!; // We know it exists from the check
          // Only add disconnect listener if we have a content script port
          if (portMap.content) {
            port?.onDisconnect.addListener(() => {
              console.log(
                `Popup port for window ${windowIdMatch} disconnected.`
              )
              // *** IMPORTANT CHECK ***
              // Check if the request still exists in the map before rejecting.
              // This prevents double-rejection if onRemoved fired first.
              if (approveMap.has(windowIdStr)) {
                console.log(
                  `Rejecting request via onDisconnect for window ${windowIdMatch}.`
                );
                portMap.content!.postMessage({ // Use non-null assertion as we checked portMap.content
                  ...approveData.eventData,
                  metadata: {
                    ...approveData.eventData.metadata,
                    from: Message.MessageFrom.BACKGROUND,
                    to: Message.MessageTo.INJECTED,
                  },
                  data: null,
                  error: walletError.userReject, // User closing the window is treated as rejection.
                })
                approveMap.delete(windowIdStr) // Clean up
              } else {
                console.log(`Request for window ${windowIdMatch} already handled (likely by onRemoved).`);
              }
            })
          } else {
            console.warn(`Popup for window ${windowIdMatch} connected, but no content script port available.`);
          }
        } else {
          console.warn(`Popup connected for window ${windowIdMatch}, but no pending request found in approveMap.`);
        }
      } else {
        console.warn(
          'Could not determine windowId for the connecting popup port.'
        )
      }
      // Assign the listener regardless of whether we found approveData initially
      portMap.popup?.onMessage.addListener(popupListener)
    } else if (port.name === Message.Port.CONTENT_BG) {
      portMap.content = port
      portMap.content?.onDisconnect.addListener(() => {
        console.log("Content script port disconnected.");
        portMap.content = undefined; // Clear the reference
        // Optionally: Handle cleanup if content script disconnects while approvals are pending
      });
      portMap.content?.onMessage.addListener(async (event: any) => {
        console.log('content port onMessage', event);

        if (!isWasmReady) {
          console.log('WASM not ready, queuing message')
          messageQueue.push({ port, event })
          return
        }

        await handleMessage(port, event)
      })
    } else if (port.name === 'KEEP_ALIVE_INTERVAL') {
      console.log('Keep alive interval port connected.')
      port.postMessage({ type: 'KEEP_ALIVE', payload: 'PONG' })
    }
  })

  // +++ NEW LISTENER: Handle window removal +++
  browser.windows.onRemoved.addListener((closedWindowId) => {
    console.log(`Window ${closedWindowId} was removed.`);
    const windowIdStr = closedWindowId.toString();

    // *** IMPORTANT CHECK ***
    // Check if this closed window corresponds to a pending approval in our map.
    if (approveMap.has(windowIdStr)) {
      const approveData = approveMap.get(windowIdStr)!; // We know it exists

      // Check if the content script port is still available
      if (portMap.content) {
        console.log(
          `Rejecting request via onRemoved for window ${closedWindowId}.`);
        // Send rejection message back to the content script
        portMap.content.postMessage({
          ...approveData.eventData, // Include original request details
          metadata: {
            ...approveData.eventData.metadata,
            from: Message.MessageFrom.BACKGROUND,
            to: Message.MessageTo.INJECTED,
          },
          data: null, // No data on rejection
          error: walletError.userReject, // User closing the window is treated as rejection.
        });

        // Clean up the pending request map
        approveMap.delete(windowIdStr);
      } else {
        console.warn(`Window ${closedWindowId} removed, but no content script port to send rejection.`);
        // Still clean up the map entry
        approveMap.delete(windowIdStr);
      }
    } else {
      console.log(`Window ${closedWindowId} removed, but no corresponding request found (might be already handled or unrelated).`);
    }
  });
  // +++ END NEW LISTENER +++

  // 启动WASM加载
  loadWalletWasm().catch(error => {
    console.error('Failed to initialize WASM:', error)
  })

  console.log('Background service worker ready.')
})

