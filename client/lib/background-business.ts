import service from '@/lib/service'
import { Message } from '@/types/message'

/**
 * 统一处理 background 的业务逻辑
 * @param action 消息动作
 * @param data 消息数据
 * @returns 业务处理结果
 */
export async function handleBackgroundAction(action: Message.MessageAction, data: any) {
  let resData = null
  let errData = null
  let reqErr: Error | undefined, reqRes: any | undefined

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
      [reqErr, reqRes] = await service.extractTxFromPsbt(
        data.psbtHex,
        data.chain
      )
      if (reqErr || !reqRes) {
        errData = {
          code: -22,
          message: reqErr?.message || '提取交易失败',
        }
      } else {
        resData = reqRes.tx
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
      [reqErr, reqRes] = await service.pushPsbt(data.psbtHex)
      if (reqErr) {
        errData = {
          code: -22,
          message: reqErr.message,
        }
      } else {
        resData = reqRes
      }
      break
    case Message.MessageAction.FINALIZE_SELL_ORDER:
      [reqErr, reqRes] = await service.finalizeSellOrder_SatsNet(
        data.psbtHex,
        data.utxos,
        data.buyerAddress,
        data.serverAddress,
        data.network,
        data.serviceFee,
        data.networkFee
      )
      if (reqErr) {
        errData = {
          code: -22,
          message: reqErr.message,
        }
      } else {
        resData = reqRes
      }
      break
    case Message.MessageAction.MERGE_BATCH_SIGNED_PSBT:
      resData = await service.mergeBatchSignedPsbt_SatsNet(
        data.psbts,
        data.network
      )
      break
    case Message.MessageAction.ADD_INPUTS_TO_PSBT:
      [reqErr, reqRes] = await service.addInputsToPsbt(
        data.psbtHex,
        data.utxos
      )
      if (reqErr) {
        errData = {
          code: -22,
          message: reqErr.message,
        }
      } else {
        resData = reqRes
      }
      break
    case Message.MessageAction.ADD_OUTPUTS_TO_PSBT:
      [reqErr, reqRes] = await service.addOutputsToPsbt(
        data.psbtHex,
        data.utxos
      )
      if (reqErr) {
        errData = {
          code: -22,
          message: reqErr.message,
        }
      } else {
        resData = reqRes
      }
      break
    // --- Added Cases for REQUEST ---
    case Message.MessageAction.GET_ALL_LOCKED_UTXO:
      [reqErr, reqRes] = await service.getAllLockedUtxo(data.address)
      if (reqErr) {
        errData = { code: -30, message: reqErr.message }
      } else {
        resData = reqRes
      }
      break
    case Message.MessageAction.GET_ALL_LOCKED_UTXO_SATSNET:
      [reqErr, reqRes] = await service.getAllLockedUtxo_SatsNet(data.address)
      if (reqErr) {
        errData = { code: -31, message: reqErr.message }
      } else {
        resData = reqRes
      }
      break
    // --- End Added Cases ---
    // --- Added Cases for UTXO Getters ---
    case Message.MessageAction.GET_UTXOS:
      [reqErr, reqRes] = await service.getUtxos()
      if (reqErr) { errData = { code: -40, message: reqErr.message } } else { resData = reqRes }
      break
    case Message.MessageAction.GET_UTXOS_SATSNET:
      [reqErr, reqRes] = await service.getUtxos_SatsNet()
      if (reqErr) { errData = { code: -41, message: reqErr.message } } else { resData = reqRes }
      break
    case Message.MessageAction.GET_UTXOS_WITH_ASSET:
      [reqErr, reqRes] = await service.getUtxosWithAsset(data.address, data.amt, data.assetName)
      if (reqErr) { errData = { code: -42, message: reqErr.message } } else { resData = reqRes }
      break
    case Message.MessageAction.GET_UTXOS_WITH_ASSET_SATSNET:
      [reqErr, reqRes] = await service.getUtxosWithAsset_SatsNet(data.address, data.amt, data.assetName)
      if (reqErr) { errData = { code: -43, message: reqErr.message } } else { resData = reqRes }
      break
    case Message.MessageAction.GET_UTXOS_WITH_ASSET_V2:
      [reqErr, reqRes] = await service.getUtxosWithAssetV2(data.address, data.amt, data.assetName)
      if (reqErr) { errData = { code: -44, message: reqErr.message } } else { resData = reqRes }
      break
    case Message.MessageAction.GET_UTXOS_WITH_ASSET_V2_SATSNET:
      [reqErr, reqRes] = await service.getUtxosWithAssetV2_SatsNet(data.address, data.amt, data.assetName)
      if (reqErr) { errData = { code: -45, message: reqErr.message } } else { resData = reqRes }
      break
    case Message.MessageAction.GET_ASSET_AMOUNT:
      [reqErr, reqRes] = await service.getAssetAmount(data.address, data.assetName)
      if (reqErr) { errData = { code: -46, message: reqErr.message } } else { resData = reqRes }
      break
    case Message.MessageAction.GET_ASSET_AMOUNT_SATSNET:
      [reqErr, reqRes] = await service.getAssetAmount_SatsNet(data.address, data.assetName)
      if (reqErr) { errData = { code: -47, message: reqErr.message } } else { resData = reqRes }
      break
    case Message.MessageAction.LOCK_UTXO_SATSNET:
      [reqErr, reqRes] = await service.lockUtxo_SatsNet(data.address, data.utxo, data.reason)
      if (reqErr) { errData = { code: -48, message: reqErr.message } } else { resData = { success: true } }
      break
    case Message.MessageAction.GET_TICKER_INFO:
      [reqErr, reqRes] = await service.getTickerInfo(data.asset)
      if (reqErr) {
        errData = { code: -50, message: reqErr.message }
      } else {
        resData = reqRes
      }
      break
    default:
      errData = { code: -32601, message: 'Method not found' }
      break
  }
  return { resData, errData }
}

/**
 * 处理 APPROVE 类型消息的业务逻辑
 */
export async function handleApproveAction(action: Message.MessageAction, event: any, context: any) {
  const { origin } = event.metadata
  const { approveMap, browser, createPopup } = context
  let createdWindowId = null

  const REQUIRES_APPROVAL = [
    Message.MessageAction.REQUEST_ACCOUNTS,
    Message.MessageAction.SWITCH_NETWORK,
    Message.MessageAction.SEND_BITCOIN,
    Message.MessageAction.SIGN_MESSAGE,
    Message.MessageAction.SIGN_PSBT,
    Message.MessageAction.SIGN_PSBTS,
    Message.MessageAction.SEND_INSCRIPTION,
    Message.MessageAction.SPLIT_ASSET,
    Message.MessageAction.LOCK_UTXO,
    Message.MessageAction.UNLOCK_UTXO,
    Message.MessageAction.UNLOCK_UTXO_SATSNET,
    Message.MessageAction.LOCK_TO_CHANNEL,
    Message.MessageAction.UNLOCK_FROM_CHANNEL,
    Message.MessageAction.BATCH_SEND_ASSETS_SATSNET,
  ]

  if (REQUIRES_APPROVAL.includes(action)) {
    // 关闭同源的旧窗口
    const windowsToRemove: number[] = []
    for (const [windowIdStr, data] of approveMap.entries()) {
      if (data.eventData.metadata.origin === origin) {
        windowsToRemove.push(data.windowId)
      }
    }
    for (const winId of windowsToRemove) {
      try {
        await browser.windows.remove(winId)
        approveMap.delete(winId.toString())
      } catch (error) {
        approveMap.delete(winId.toString())
      }
    }
    const newWindow = await createPopup(
      browser.runtime.getURL(`/popup.html#/wallet/approve`)
    )
    if (newWindow?.id) {
      createdWindowId = newWindow.id
      approveMap.set(createdWindowId.toString(), {
        windowId: createdWindowId,
        eventData: event,
      })
      return { createdWindowId }
    } else {
      throw new Error('Failed to create approval popup')
    }
  } else {
    throw new Error('Action does not require approval')
  }
}

/**
 * popupListener 业务逻辑
 */
export async function handlePopupListener(message: any, context: any) {
  const { portMap, approveMap, browser, walletError } = context
  const eventData = message
  const { action, metadata = {} } = eventData
  const { windowId, from } = metadata

  if (from === Message.MessageFrom.POPUP) {
    if (action === Message.MessageAction.APPROVE_RESPONSE) {
      eventData.metadata.from = Message.MessageFrom.BACKGROUND
      eventData.metadata.to = Message.MessageTo.INJECTED
      if (portMap.content) {
        await portMap.content.postMessage(eventData)
      }
      if (windowId) {
        approveMap.delete(windowId.toString())
        try {
          await browser.windows.remove(windowId)
        } catch {}
      }
    } else if (action === Message.MessageAction.REJECT_RESPONSE) {
      eventData.metadata.from = Message.MessageFrom.BACKGROUND
      eventData.metadata.to = Message.MessageTo.INJECTED
      if (portMap.content) {
        portMap.content.postMessage({
          ...eventData,
          error: walletError.userReject,
        })
      }
      if (windowId) {
        approveMap.delete(windowId.toString())
        try {
          await browser.windows.remove(windowId)
        } catch {}
      }
    }
  }
}

/**
 * onMessage 业务逻辑
 */
export async function handleRuntimeOnMessage(message: any, context: any) {
  const { approveMap, Message, getConfig, logLevel, browser } = context
  const { type, action, metadata } = message
  if (
    type === Message.MessageType.REQUEST &&
    action === Message.MessageAction.GET_APPROVE_DATA &&
    metadata?.windowId
  ) {
    const { windowId } = metadata
    const approveData = approveMap.get(windowId.toString())
    if (approveData) {
      return {
        action: Message.MessageAction.GET_APPROVE_DATA_RESPONSE,
        data: approveData.eventData,
      }
    } else {
      return undefined
    }
  } else if (type === Message.MessageType.REQUEST && action === Message.MessageAction.ENV_CHANGED) {
    await (globalThis as any).stp_wasm.release()
    await (globalThis as any).stp_wasm.init(getConfig(message.data.env), logLevel)
    await (globalThis as any).sat20wallet_wasm.release()
    await (globalThis as any).sat20wallet_wasm.init(getConfig(message.data.env), logLevel)
    return undefined
  }
  return undefined
}

/**
 * window remove 业务逻辑
 */
export function handleWindowRemoved(closedWindowId: number, context: any) {
  const { approveMap, portMap, walletError, Message } = context
  const windowIdStr = closedWindowId.toString()
  if (approveMap.has(windowIdStr)) {
    const approveData = approveMap.get(windowIdStr)!
    if (portMap.content) {
      portMap.content.postMessage({
        ...approveData.eventData,
        metadata: {
          ...approveData.eventData.metadata,
          from: Message.MessageFrom.BACKGROUND,
          to: Message.MessageTo.INJECTED,
        },
        data: null,
        error: walletError.userReject,
      })
      approveMap.delete(windowIdStr)
    } else {
      approveMap.delete(windowIdStr)
    }
  }
} 