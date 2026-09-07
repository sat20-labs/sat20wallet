import { Message } from "../../../types/message";
import { ApprovalHandler } from "../utils/approval-handler";
import { ResponseHandler } from "../utils/response-handler";

export class AssetHandlers {
  constructor(
    private approvalHandler: ApprovalHandler,
    private responseHandler: ResponseHandler,
    private currentUrl: () => string
  ) {}

  /**
   * 处理 BATCH_SEND_ASSETS_SATSNET - 需要用户授权
   */
  async handleBatchSendAssetsSatsNet(callbackId: string, data: any): Promise<void> {
    try {
      console.log("📦 Handling BATCH_SEND_ASSETS_SATSNET", { callbackId });
      const result = await this.approvalHandler.handleWalletApproval(
        Message.MessageAction.BATCH_SEND_ASSETS_SATSNET,
        data,
        callbackId,
        this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ BATCH_SEND_ASSETS_SATSNET error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 BATCH_SEND_ASSETS_V2_SATSNET - 需要用户授权
   */
  async handleBatchSendAssetsV2SatsNet(callbackId: string, data: any): Promise<void> {
    try {
      console.log("📦 Handling BATCH_SEND_ASSETS_V2_SATSNET", { callbackId });
      const result = await this.approvalHandler.handleWalletApproval(
        Message.MessageAction.BATCH_SEND_ASSETS_V2_SATSNET,
        data,
        callbackId,
        this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ BATCH_SEND_ASSETS_V2_SATSNET error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 SEND_ASSETS_SATSNET - 需要用户授权
   */
  async handleSendAssetsSatsNet(callbackId: string, data: any): Promise<void> {
    try {
      console.log("💸 Handling SEND_ASSETS_SATSNET", { callbackId });
      const result = await this.approvalHandler.handleWalletApproval(
        Message.MessageAction.SEND_ASSETS_SATSNET,
        data,
        callbackId,
        this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ SEND_ASSETS_SATSNET error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 SPLIT_ASSET - 需要用户授权
   */
  async handleSplitAsset(callbackId: string, data: any): Promise<void> {
    try {
      console.log("✂️ Handling SPLIT_ASSET", { callbackId });
      const result = await this.approvalHandler.handleWalletApproval(
        Message.MessageAction.SPLIT_ASSET,
        data,
        callbackId,
        this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ SPLIT_ASSET error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 UTXO 相关操作 - 直接请求类型
   */
  async handleGetUtxosWithAsset(callbackId: string, data: any): Promise<void> {
    try {
      console.log("🔍 Handling GET_UTXOS_WITH_ASSET", { callbackId, data });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.GET_UTXOS_WITH_ASSET,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ GET_UTXOS_WITH_ASSET error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  async handleGetUtxosWithAssetSatsNet(callbackId: string, data: any): Promise<void> {
    try {
      console.log("🔍 Handling GET_UTXOS_WITH_ASSET_SATSNET", { callbackId, data });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.GET_UTXOS_WITH_ASSET_SATSNET,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ GET_UTXOS_WITH_ASSET_SATSNET error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  async handleGetUtxosWithAssetV2(callbackId: string, data: any): Promise<void> {
    try {
      console.log("🔍 Handling GET_UTXOS_WITH_ASSET_V2", { callbackId, data });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.GET_UTXOS_WITH_ASSET_V2,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ GET_UTXOS_WITH_ASSET_V2 error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  async handleGetUtxosWithAssetV2SatsNet(callbackId: string, data: any): Promise<void> {
    try {
      console.log("🔍 Handling GET_UTXOS_WITH_ASSET_V2_SATSNET", { callbackId, data });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.GET_UTXOS_WITH_ASSET_V2_SATSNET,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ GET_UTXOS_WITH_ASSET_V2_SATSNET error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  async handleGetAssetAmount(callbackId: string, data: any): Promise<void> {
    try {
      console.log("💰 Handling GET_ASSET_AMOUNT", { callbackId, data });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.GET_ASSET_AMOUNT,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ GET_ASSET_AMOUNT error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  async handleGetAssetAmountSatsNet(callbackId: string, data: any): Promise<void> {
    try {
      console.log("💰 Handling GET_ASSET_AMOUNT_SATSNET", { callbackId, data });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.GET_ASSET_AMOUNT_SATSNET,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ GET_ASSET_AMOUNT_SATSNET error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 UTXO 锁定/解锁操作
   */
  async handleLockUtxo(callbackId: string, data: any): Promise<void> {
    try {
      console.log("🔒 Handling LOCK_UTXO", { callbackId });
      const result = await this.approvalHandler.handleApprovedDirectRequest(
        Message.MessageAction.LOCK_UTXO, data, callbackId, this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ LOCK_UTXO error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  async handleUnlockUtxo(callbackId: string, data: any): Promise<void> {
    try {
      console.log("🔓 Handling UNLOCK_UTXO", { callbackId });
      const result = await this.approvalHandler.handleApprovedDirectRequest(
        Message.MessageAction.UNLOCK_UTXO, data, callbackId, this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ UNLOCK_UTXO error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  async handleLockUtxoSatsNet(callbackId: string, data: any): Promise<void> {
    try {
      const result = await this.approvalHandler.handleApprovedDirectRequest(
        Message.MessageAction.LOCK_UTXO_SATSNET, data, callbackId, this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  async handleUnlockUtxoSatsNet(callbackId: string, data: any): Promise<void> {
    try {
      const result = await this.approvalHandler.handleApprovedDirectRequest(
        Message.MessageAction.UNLOCK_UTXO_SATSNET, data, callbackId, this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  async handleGetAllLockedUtxo(callbackId: string, data: any): Promise<void> {
    try {
      console.log("🔍 Handling GET_ALL_LOCKED_UTXO", { callbackId, data });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.GET_ALL_LOCKED_UTXO,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ GET_ALL_LOCKED_UTXO error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }
}
