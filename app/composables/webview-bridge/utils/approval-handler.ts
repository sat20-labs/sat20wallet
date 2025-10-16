import { useApproveStore } from "../../../store";
import { Message } from "../../../types/message";
import { ApprovalMetadata } from "../types";
import { BrowserManager } from "./browser-manager";
import { LOG_PREFIXES } from "../constants";
import { walletStorage } from "@/lib/walletStorage";
import service from "@/lib/service";
import sat20Wallet from "@/utils/sat20";

export class ApprovalHandler {
  constructor(private browserManager: BrowserManager) {}

  /**
   * 处理需要用户授权的操作
   */
  async handleWalletApproval<T>(
    action: Message.MessageAction,
    data: any,
    callbackId: string,
    currentUrl: string
  ): Promise<T> {
    try {
      console.log(`${LOG_PREFIXES.WALLET_APPROVAL} Starting wallet approval for ${action}`, {
        action,
        data,
        callbackId,
      });

      // 隐藏InAppBrowser以便显示钱包弹窗
      this.browserManager.hideBrowser();

      const approveStore = useApproveStore();

      // 构建完整的metadata，包含必要的origin信息
      let origin = "inappbrowser";
      if (currentUrl) {
        try {
          origin = new URL(currentUrl).origin;
        } catch (error) {
          console.warn(
            "⚠️ Failed to parse URL for origin:",
            currentUrl,
            error
          );
          origin = "inappbrowser";
        }
      }

      const metadata: ApprovalMetadata = {
        callbackId,
        origin,
        dAppOrigin: "inappbrowser",
        platform: "inappbrowser",
        url: currentUrl,
      };

      console.log(`📋 Approval metadata:`, metadata);

      // 使用全局弹窗显示授权请求
      const result = await approveStore.showApprove({
        action,
        data: { ...data, callbackId, dAppOrigin: "inappbrowser" },
        metadata,
      });

      console.log(`✅ ${action} approved:`, result);

      // 显示InAppBrowser
      this.browserManager.showBrowser();

      return result as T;
    } catch (error) {
      console.error(`❌ ${action} rejected:`, error);

      // 确保显示InAppBrowser，即使用户拒绝了
      this.browserManager.showBrowser();

      throw error;
    }
  }

  /**
   * 处理直接请求类型操作（无需授权）
   */
  async handleDirectRequest<T>(
    action: Message.MessageAction,
    data: any
  ): Promise<T> {
    try {
      console.log(`${LOG_PREFIXES.DIRECT_REQUEST} Handling direct request: ${action}`, { action, data });

      // 确保钱包状态已初始化
      await walletStorage.initializeState();
      const hasWallet = await service.getHasWallet();
      if (!hasWallet) {
        throw new Error("No wallet available");
      }

      let result: any = null;

      switch (action) {
        case Message.MessageAction.GET_ACCOUNTS:
          result = await service.getAccounts();
          break;
        case Message.MessageAction.GET_PUBLIC_KEY:
          result = await service.getPublicKey();
          break;
        case Message.MessageAction.GET_NETWORK:
          result = await service.getNetwork();
          break;
        case Message.MessageAction.GET_BALANCE:
          result = await service.getBalance();
          break;
        case Message.MessageAction.GET_UTXOS:
          const [utxoErr, utxoRes] = await service.getUtxos();
          if (utxoErr) throw utxoErr;
          result = utxoRes;
          break;
        case Message.MessageAction.GET_UTXOS_SATSNET:
          const [utxoSNErr, utxoSNRes] = await service.getUtxos_SatsNet();
          if (utxoSNErr) throw utxoSNErr;
          result = utxoSNRes;
          break;
        case Message.MessageAction.GET_ALL_LOCKED_UTXO:
          const [lockedErr, lockedRes] = await service.getAllLockedUtxo(data.address);
          if (lockedErr) throw lockedErr;
          result = lockedRes;
          break;
        case Message.MessageAction.GET_ALL_LOCKED_UTXO_SATSNET:
          const [lockedSNErr, lockedSNRes] = await service.getAllLockedUtxo_SatsNet(data.address);
          if (lockedSNErr) throw lockedSNErr;
          result = lockedSNRes;
          break;
        case Message.MessageAction.GET_CURRENT_NAME:
          result = await service.getCurrentName(data.address);
          break;
        case Message.MessageAction.GET_FEE_FOR_DEPLOY_CONTRACT:
          const [deployErr, deployRes] = await service.getFeeForDeployContract(
            data.templateName,
            data.content,
            data.feeRate
          );
          if (deployErr) throw deployErr;
          result = deployRes;
          break;
        case Message.MessageAction.GET_FEE_FOR_INVOKE_CONTRACT:
          const [invokeErr, invokeRes] = await service.getFeeForInvokeContract(
            data.url,
            data.invoke
          );
          if (invokeErr) throw invokeErr;
          result = invokeRes;
          break;
        case Message.MessageAction.GET_ASSET_AMOUNT:
          const [amountErr, amountRes] = await service.getAssetAmount(
            data.address,
            data.assetName
          );
          if (amountErr) throw amountErr;
          result = amountRes;
          break;
        case Message.MessageAction.GET_ASSET_AMOUNT_SATSNET:
          const [amountSNErr, amountSNRes] = await service.getAssetAmount_SatsNet(data.address, data.assetName);
          if (amountSNErr) throw amountSNErr;
          result = amountSNRes;
          break;
        case Message.MessageAction.GET_UTXOS_WITH_ASSET:
          const [assetErr, assetRes] = await service.getUtxosWithAsset(
            data.address,
            data.amt,
            data.assetName
          );
          if (assetErr) throw assetErr;
          result = assetRes;
          break;
        case Message.MessageAction.GET_UTXOS_WITH_ASSET_SATSNET:
          const [assetSNErr, assetSNRes] = await service.getUtxosWithAsset_SatsNet(
            data.address,
            data.amt,
            data.assetName
          );
          if (assetSNErr) throw assetSNErr;
          result = assetSNRes;
          break;
        case Message.MessageAction.GET_UTXOS_WITH_ASSET_V2:
          const [assetV2Err, assetV2Res] = await service.getUtxosWithAssetV2(
            data.address,
            data.amt,
            data.assetName
          );
          if (assetV2Err) throw assetV2Err;
          result = assetV2Res;
          break;
        case Message.MessageAction.GET_UTXOS_WITH_ASSET_V2_SATSNET:
          const [assetV2SNErr, assetV2SNRes] = await service.getUtxosWithAssetV2_SatsNet(
            data.address,
            data.amt,
            data.assetName
          );
          if (assetV2SNErr) throw assetV2SNErr;
          result = assetV2SNRes;
          break;
        case Message.MessageAction.BUILD_BATCH_SELL_ORDER:
          const [buildErr, buildRes] = await service.buildBatchSellOrder_SatsNet(
            data.utxos,
            data.address,
            data.network
          );
          if (buildErr) throw buildErr;
          result = buildRes;
          break;
        case Message.MessageAction.SPLIT_BATCH_SIGNED_PSBT_SATSNET:
          const [splitErr, splitRes] = await service.splitBatchSignedPsbt_SatsNet(
            data.signedHex,
            data.network
          );
          if (splitErr) throw splitErr;
          result = splitRes;
          break;
        case Message.MessageAction.FINALIZE_SELL_ORDER:
          const [finalizeErr, finalizeRes] = await service.finalizeSellOrder_SatsNet(
            data.psbtHex,
            data.utxos,
            data.buyerAddress,
            data.serverAddress,
            data.network,
            data.serviceFee,
            data.networkFee
          );
          if (finalizeErr) throw finalizeErr;
          result = finalizeRes;
          break;
        case Message.MessageAction.MERGE_BATCH_SIGNED_PSBT:
          const [mergeErr, mergeRes] = await service.mergeBatchSignedPsbt_SatsNet(
            data.psbts,
            data.network
          );
          if (mergeErr) throw mergeErr;
          result = mergeRes;
          break;
        case Message.MessageAction.ADD_INPUTS_TO_PSBT:
          const [addInputsErr, addInputsRes] = await service.addInputsToPsbt(
            data.psbtHex,
            data.utxos
          );
          if (addInputsErr) throw addInputsErr;
          result = addInputsRes;
          break;
        case Message.MessageAction.ADD_OUTPUTS_TO_PSBT:
          const [addOutputsErr, addOutputsRes] = await service.addOutputsToPsbt(
            data.psbtHex,
            data.utxos
          );
          if (addOutputsErr) throw addOutputsErr;
          result = addOutputsRes;
          break;
        case Message.MessageAction.EXTRACT_TX_FROM_PSBT:
          const [extractErr, extractRes] = await service.extractTxFromPsbt(
            data.psbtHex,
            { chain: data.chain }
          );
          if (extractErr) throw extractErr;
          result = extractRes;
          break;
        case Message.MessageAction.EXTRACT_TX_FROM_PSBT_SATSNET:
          // 使用 sat20Wallet 直接调用，因为 service 中没有此方法
          const [extractSNErr, extractSNRes] = await sat20Wallet.extractTxFromPsbt_SatsNet(
            data.psbtHex
          );
          if (extractSNErr) throw extractSNErr;
          result = extractSNRes;
          break;
        case Message.MessageAction.PUSH_TX:
          const [pushTxErr, pushTxRes] = await service.pushTx(data.rawtx);
          if (pushTxErr) throw pushTxErr;
          result = pushTxRes;
          break;
        case Message.MessageAction.PUSH_PSBT:
          const [pushPsbtErr, pushPsbtRes] = await service.pushPsbt(data.psbtHex);
          if (pushPsbtErr) throw pushPsbtErr;
          result = pushPsbtRes;
          break;
        case Message.MessageAction.QUERY_PARAM_FOR_INVOKE_CONTRACT:
          const [paramErr, paramRes] = await service.getParamForInvokeContract(
            data.templateName,
            data.action
          );
          if (paramErr) throw paramErr;
          result = paramRes;
          break;
        case Message.MessageAction.GET_INSCRIPTIONS:
          // TODO: Implement getInscriptions method in service
          console.warn("⚠️ GET_INSCRIPTIONS method not implemented in service");
          result = [];
          break;
        default:
          throw new Error(`Unsupported direct request action: ${action}`);
      }

      console.log(`✅ Direct request ${action} completed:`, result);
      return result as T;
    } catch (error) {
      console.error(`❌ Direct request ${action} failed:`, error);
      throw error;
    }
  }
}