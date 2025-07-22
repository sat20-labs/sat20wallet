import type { Runtime } from 'wxt/browser'
import { Message } from '@/types/message'
import { walletError } from '@/types/error'
import { isOriginAuthorized } from '@/lib/authorized-origins'
import { walletStorage } from '@/lib/walletStorage'
import service from '@/lib/service'
import type { ApprovalManager } from './ApprovalManager'

// The main message handler, moved from background.ts
export async function handleMessage(
  port: Runtime.Port,
  event: any,
  approvalManager: ApprovalManager,
) {
  const eventData = event
  const { action, type, data } = eventData
  const { origin } = eventData.metadata

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
      Message.MessageAction.MERGE_BATCH_SIGNED_PSBT,
      // --- 名字管理相关 ---
      Message.MessageAction.GET_CURRENT_NAME,
    ]

    // Verify origin authorization for specific methods.
    if (METHODS_REQUIRING_AUTHORIZATION.includes(action)) {
      const authorized = await isOriginAuthorized(origin)
      if (!authorized) {
        port.postMessage({
          // Use port directly
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
            data.network,
          )
          break
        case Message.MessageAction.SPLIT_BATCH_SIGNED_PSBT:
          resData = await service.splitBatchSignedPsbt(
            data.signedHex,
            data.network,
          )
          break
        case Message.MessageAction.SPLIT_BATCH_SIGNED_PSBT_SATSNET:
          resData = await service.splitBatchSignedPsbt_SatsNet(
            data.signedHex,
            data.network,
          )
          break
        case Message.MessageAction.EXTRACT_TX_FROM_PSBT:
          const [extractErr, extractRes] = await service.extractTxFromPsbt(
            data.psbtHex,
            data.chain,
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
          const [finalizeErr, finalizeRes] =
            await service.finalizeSellOrder_SatsNet(
              data.psbtHex,
              data.utxos,
              data.buyerAddress,
              data.serverAddress,
              data.network,
              data.serviceFee,
              data.networkFee,
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
            data.network,
          )
          break
        case Message.MessageAction.ADD_INPUTS_TO_PSBT:
          const [inputsErr, inputsRes] = await service.addInputsToPsbt(
            data.psbtHex,
            data.utxos,
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
            data.utxos,
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
          ;[reqErr, reqRes] = await service.getAllLockedUtxo(data.address)
          if (reqErr) {
            errData = { code: -30, message: reqErr.message } // Example error code
          } else {
            resData = reqRes
          }
          break
        case Message.MessageAction.GET_ALL_LOCKED_UTXO_SATSNET:
          ;[reqErr, reqRes] = await service.getAllLockedUtxo_SatsNet(
            data.address,
          )
          if (reqErr) {
            errData = { code: -31, message: reqErr.message } // Example error code
          } else {
            resData = reqRes
          }
          break
        // --- End Added Cases ---

        // --- Added Cases for UTXO Getters ---
        case Message.MessageAction.GET_UTXOS:
          ;[reqErr, reqRes] = await service.getUtxos()
          if (reqErr) {
            errData = { code: -40, message: reqErr.message }
          } else {
            resData = reqRes
          }
          break
        case Message.MessageAction.GET_UTXOS_SATSNET:
          ;[reqErr, reqRes] = await service.getUtxos_SatsNet()
          if (reqErr) {
            errData = { code: -41, message: reqErr.message }
          } else {
            resData = reqRes
          }
          break
        case Message.MessageAction.GET_UTXOS_WITH_ASSET:
          ;[reqErr, reqRes] = await service.getUtxosWithAsset(
            data.address,
            data.amt,
            data.assetName,
          )
          if (reqErr) {
            errData = { code: -42, message: reqErr.message }
          } else {
            resData = reqRes
          }
          break
        case Message.MessageAction.GET_UTXOS_WITH_ASSET_SATSNET:
          ;[reqErr, reqRes] = await service.getUtxosWithAsset_SatsNet(
            data.address,
            data.amt,
            data.assetName,
          )
          if (reqErr) {
            errData = { code: -43, message: reqErr.message }
          } else {
            resData = reqRes
          }
          break
        case Message.MessageAction.GET_UTXOS_WITH_ASSET_V2:
          ;[reqErr, reqRes] = await service.getUtxosWithAssetV2(
            data.address,
            data.amt,
            data.assetName,
          )
          if (reqErr) {
            errData = { code: -44, message: reqErr.message }
          } else {
            resData = reqRes
          }
          break
        case Message.MessageAction.GET_UTXOS_WITH_ASSET_V2_SATSNET:
          ;[reqErr, reqRes] = await service.getUtxosWithAssetV2_SatsNet(
            data.address,
            data.amt,
            data.assetName,
          )
          if (reqErr) {
            errData = { code: -45, message: reqErr.message }
          } else {
            resData = reqRes
          }
          break
        case Message.MessageAction.GET_ASSET_AMOUNT:
          ;[reqErr, reqRes] = await service.getAssetAmount(
            data.address,
            data.assetName,
          )
          if (reqErr) {
            errData = { code: -46, message: reqErr.message }
          } else {
            resData = reqRes
          }
          break
        case Message.MessageAction.GET_ASSET_AMOUNT_SATSNET:
          ;[reqErr, reqRes] = await service.getAssetAmount_SatsNet(
            data.address,
            data.assetName,
          )
          if (reqErr) {
            errData = { code: -47, message: reqErr.message }
          } else {
            resData = reqRes
          } // Handle potential error/result
          break
        case Message.MessageAction.LOCK_UTXO:
          ;[reqErr, reqRes] = await service.lockUtxo(
            data.address,
            data.utxo,
            data.reason,
          )
          if (reqErr) {
            errData = { code: -48, message: reqErr.message }
          } else {
            resData = { success: true }
          }
          break

        case Message.MessageAction.UNLOCK_UTXO:
          ;[reqErr, reqRes] = await service.unlockUtxo(data.address, data.utxo)
          if (reqErr) {
            errData = { code: -49, message: reqErr.message }
          } else {
            resData = { success: true }
          }
          break
        case Message.MessageAction.LOCK_UTXO_SATSNET:
          ;[reqErr, reqRes] = await service.lockUtxo_SatsNet(
            data.address,
            data.utxo,
            data.reason,
          )
          if (reqErr) {
            errData = { code: -48, message: reqErr.message }
          } else {
            resData = { success: true }
          }
          break
        case Message.MessageAction.UNLOCK_UTXO_SATSNET:
          ;[reqErr, reqRes] = await service.unlockUtxo_SatsNet(
            data.address,
            data.utxo,
          )
          if (reqErr) {
            errData = { code: -49, message: reqErr.message }
          } else {
            resData = { success: true }
          }
          break

        // --- 合约相关方法 ---
        case Message.MessageAction.GET_FEE_FOR_DEPLOY_CONTRACT:
          ;[reqErr, reqRes] = await service.getFeeForDeployContract(
            data.templateName,
            data.content,
            data.feeRate,
          )
          if (reqErr) {
            errData = { code: -63, message: reqErr.message }
          } else {
            resData = reqRes
          }
          break
        case Message.MessageAction.QUERY_PARAM_FOR_INVOKE_CONTRACT:
          ;[reqErr, reqRes] = await service.getParamForInvokeContract(
            data.templateName,
            data.action,
          )
          if (reqErr) {
            errData = { code: -64, message: reqErr.message }
          } else {
            resData = reqRes
          }
          break
        case Message.MessageAction.GET_FEE_FOR_INVOKE_CONTRACT:
          ;[reqErr, reqRes] = await service.getFeeForInvokeContract(
            data.url,
            data.invoke,
          )
          if (reqErr) {
            errData = { code: -65, message: reqErr.message }
          } else {
            resData = reqRes
          }
          break
        case Message.MessageAction.GET_CURRENT_NAME:
          resData = await service.getCurrentName(data.address)
          break
        default:
          console.warn(`Unhandled REQUEST action: ${action}`)
          errData = { code: -32601, message: 'Method not found' } // Method not found error
          break
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
        // --- 合约相关 ---
        Message.MessageAction.DEPLOY_CONTRACT_REMOTE,
        Message.MessageAction.INVOKE_CONTRACT_SATSNET,
        Message.MessageAction.INVOKE_CONTRACT_V2_SATSNET,
        Message.MessageAction.INVOKE_CONTRACT_V2,
        // --- 推荐人相关 ---
        Message.MessageAction.REGISTER_AS_REFERRER,
        Message.MessageAction.BIND_REFERRER_FOR_SERVER,
      ]

      if (REQUIRES_APPROVAL.includes(action)) {
        try {
          await approvalManager.requestApproval(event)
        } catch (error: any) {
          console.error('创建审批弹窗时出错:', error)
          port.postMessage({
            // Use port directly
            ...eventData,
            error: {
              code: -1,
              message: error.message || '创建审批弹窗失败',
            },
            data: null,
          })
        }
      } else {
        // Handle cases where APPROVE type is received but action doesn't require it
        console.warn(`收到无需审批的动作的 APPROVE 消息: ${action}`)
        port.postMessage({
          // Use port directly
          ...eventData,
          error: {
            code: -32600,
            message: '无效请求：此操作不需要审批。',
          },
          data: null,
        })
      }
    }
  } catch (error: any) {
    // Add type annotation for error
    console.error('Error handling message:', error)
    port.postMessage({
      // Use port directly
      ...eventData,
      data: null,
      error: {
        code: -32603, // Internal error code
        message: error?.message || '处理消息时发生内部错误', // Include error message if available
      },
    })
  }
} 