import { useApproveStore } from "../../../store";
import { Message } from "../../../types/message";
import { ApprovalMetadata } from "../types";
import { BrowserManager } from "./browser-manager";
import { walletStorage } from "@/lib/walletStorage";
import service from "@/lib/service";
import sat20Wallet from "@/utils/sat20";
import { getDappActionPolicy, hasApprovalRenderer } from "@/lib/dapp-policy";
import { assertWalletIdentityReady } from "@/lib/identity-boundary";
import { assertNoMnemonicView } from "@/lib/sensitive-session";

export interface ApprovalRequestContext {
  origin: string;
  url?: string;
  expiresAt?: number;
  identityGeneration?: number;
}

export class ApprovalHandler {
  private readonly approvedDirectToken = Symbol("approved-dapp-operation");

  constructor(private browserManager: BrowserManager) {}

  /**
   * 处理需要用户授权的操作
   */
  async handleWalletApproval<T>(
    action: Message.MessageAction,
    data: any,
    callbackId: string,
    currentUrl: string | ApprovalRequestContext
  ): Promise<T> {
    const approveStore = useApproveStore();
    try {
	  assertNoMnemonicView();
	  const identityGeneration = assertWalletIdentityReady(
		  typeof currentUrl === "string" ? undefined : currentUrl.identityGeneration
	  );
      if (!hasApprovalRenderer(action)) {
        throw new Error(`Wallet approval renderer is unavailable for ${action}`);
      }

      // 隐藏InAppBrowser以便显示钱包弹窗
      this.browserManager.hideBrowser();

      // 构建完整的metadata，包含必要的origin信息
      let origin = typeof currentUrl === "string" ? "inappbrowser" : currentUrl.origin;
      const url = typeof currentUrl === "string" ? currentUrl : (currentUrl.url ?? currentUrl.origin);
      if (typeof currentUrl === "string" && currentUrl) {
        try {
          origin = new URL(currentUrl).origin;
        } catch {
          throw new Error("Invalid DApp approval origin");
        }
      }

      const metadata: ApprovalMetadata = {
        callbackId,
        requestId: callbackId,
        origin,
        dAppOrigin: origin,
        platform: "pwa",
        url,
        action,
        expiresAt: typeof currentUrl === "string" ? undefined : currentUrl.expiresAt,
		identityGeneration,
      };
      const approvalId = `${origin}\0${callbackId}`;

      // 使用全局弹窗显示授权请求
      const result = await approveStore.showApprove({
        action,
        id: approvalId,
        data: { ...data, callbackId, dAppOrigin: origin },
        metadata,
      });
	  assertNoMnemonicView();
	  assertWalletIdentityReady(action === Message.MessageAction.SWITCH_NETWORK ? undefined : identityGeneration);

      // 显示InAppBrowser
      if (!approveStore.isVisible.value) this.browserManager.showBrowser();

      return result as T;
    } catch (error) {
      // 确保显示InAppBrowser，即使用户拒绝了
      if (!approveStore.isVisible.value) this.browserManager.showBrowser();

      throw error;
    }
  }

  async handleApprovedDirectRequest<T>(
    action: Message.MessageAction,
    data: any,
    callbackId: string,
    context: string | ApprovalRequestContext
  ): Promise<T> {
	const identityGeneration = assertWalletIdentityReady(
		typeof context === "string" ? undefined : context.identityGeneration
	);
    await this.handleWalletApproval(action, data, callbackId, context);
    return this.handleDirectRequest(action, data, this.approvedDirectToken, identityGeneration);
  }

  /**
   * 处理直接请求类型操作（无需授权）
   */
  async handleDirectRequest<T>(
    action: Message.MessageAction,
    data: any,
    approvalToken?: symbol,
	expectedGeneration?: number
  ): Promise<T> {
    try {
	  assertNoMnemonicView();
	  assertWalletIdentityReady(expectedGeneration);
      const policy = getDappActionPolicy(action);
      if (!policy) throw new Error(`Unsupported DApp action: ${action}`);
      if (policy.approval !== "none" && approvalToken !== this.approvedDirectToken) {
        throw new Error(`Per-request wallet approval is required for ${action}`);
      }
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
        case Message.MessageAction.LOCK_UTXO:
          if (!data.__dappOwner) throw new Error("DApp lock owner is required");
          const [lockErr, lockRes] = await service.lockUtxoForOwner(data.address, data.utxo, data.reason, data.__dappOwner);
          if (lockErr) throw lockErr;
          result = lockRes;
          break;
        case Message.MessageAction.LOCK_UTXO_SATSNET:
          if (!data.__dappOwner) throw new Error("DApp lock owner is required");
          const [lockSNErr, lockSNRes] = await service.lockUtxoForOwner_SatsNet(data.address, data.utxo, data.reason, data.__dappOwner);
          if (lockSNErr) throw lockSNErr;
          result = lockSNRes;
          break;
        case Message.MessageAction.UNLOCK_UTXO:
          if (!data.__dappOwner) throw new Error("DApp lock owner is required");
          const [unlockErr, unlockRes] = await service.unlockUtxoForOwner(data.address, data.utxo, data.__dappOwner);
          if (unlockErr) throw unlockErr;
          result = unlockRes;
          break;
        case Message.MessageAction.UNLOCK_UTXO_SATSNET:
          if (!data.__dappOwner) throw new Error("DApp lock owner is required");
          const [unlockSNErr, unlockSNRes] = await service.unlockUtxoForOwner_SatsNet(data.address, data.utxo, data.__dappOwner);
          if (unlockSNErr) throw unlockSNErr;
          result = unlockSNRes;
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
          const [buildErr, buildRes] = data.chain === 'btc'
            ? await service.buildBatchSellOrder(
              data.utxos,
              data.address,
              data.network
            )
            : await service.buildBatchSellOrder_SatsNet(
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
        case Message.MessageAction.BATCH_SEND_ASSETS_V2_SATSNET:
          const [batchSendV2Err, batchSendV2Res] = await sat20Wallet.batchSendAssetsV2_SatsNet(
            data.destAddr,
            data.assetName,
            data.amtList
          );
          if (batchSendV2Err) throw batchSendV2Err;
          result = batchSendV2Res;
          break;
        case Message.MessageAction.INVOKE_UNIFIED_CONTRACT:
          result = await this.handleWalletApproval(
            action,
            data,
            typeof data?.callbackId === "string" ? data.callbackId : `direct-${Date.now()}`,
            typeof data?.url === "string" ? data.url : "inappbrowser"
          );
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

	  assertNoMnemonicView();
	  assertWalletIdentityReady(expectedGeneration);
      return result as T;
    } catch (error) {
      throw error;
    }
  }
}
