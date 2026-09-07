import { Message } from "../../../types/message";
import { ApprovalHandler } from "../utils/approval-handler";
import { ResponseHandler } from "../utils/response-handler";

export class UtilityHandlers {
  constructor(
    private approvalHandler: ApprovalHandler,
    private responseHandler: ResponseHandler,
    private currentUrl: () => string
  ) {}

  /**
   * 处理 REGISTER_AS_REFERRER - 需要用户授权
   */
  async handleRegisterAsReferrer(callbackId: string, data: any): Promise<void> {
    try {
      console.log("👥 Handling REGISTER_AS_REFERRER", { callbackId });
      const result = await this.approvalHandler.handleWalletApproval(
        Message.MessageAction.REGISTER_AS_REFERRER,
        data,
        callbackId,
        this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ REGISTER_AS_REFERRER error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 BIND_REFERRER_FOR_SERVER - 需要用户授权
   */
  async handleBindReferrerForServer(callbackId: string, data: any): Promise<void> {
    try {
      console.log("🔗 Handling BIND_REFERRER_FOR_SERVER", { callbackId });
      const result = await this.approvalHandler.handleWalletApproval(
        Message.MessageAction.BIND_REFERRER_FOR_SERVER,
        data,
        callbackId,
        this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ BIND_REFERRER_FOR_SERVER error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 PSBT 操作相关的直接请求
   */
  async handleBuildBatchSellOrder(callbackId: string, data: any): Promise<void> {
    try {
      console.log("📦 Handling BUILD_BATCH_SELL_ORDER", { callbackId, data });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.BUILD_BATCH_SELL_ORDER,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ BUILD_BATCH_SELL_ORDER error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  async handleSplitBatchSignedPsbtSatsNet(callbackId: string, data: any): Promise<void> {
    try {
		console.log("✂️ Handling SPLIT_BATCH_SIGNED_PSBT_SATSNET", { callbackId });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.SPLIT_BATCH_SIGNED_PSBT_SATSNET,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ SPLIT_BATCH_SIGNED_PSBT_SATSNET error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  async handleFinalizeSellOrder(callbackId: string, data: any): Promise<void> {
    try {
      console.log("🏁 Handling FINALIZE_SELL_ORDER", { callbackId, data });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.FINALIZE_SELL_ORDER,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ FINALIZE_SELL_ORDER error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  async handleMergeBatchSignedPsbt(callbackId: string, data: any): Promise<void> {
    try {
		console.log("🔗 Handling MERGE_BATCH_SIGNED_PSBT", { callbackId });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.MERGE_BATCH_SIGNED_PSBT,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ MERGE_BATCH_SIGNED_PSBT error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  async handleAddInputsToPsbt(callbackId: string, data: any): Promise<void> {
    try {
		console.log("➕ Handling ADD_INPUTS_TO_PSBT", { callbackId });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.ADD_INPUTS_TO_PSBT,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ ADD_INPUTS_TO_PSBT error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  async handleAddOutputsToPsbt(callbackId: string, data: any): Promise<void> {
    try {
		console.log("➕ Handling ADD_OUTPUTS_TO_PSBT", { callbackId });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.ADD_OUTPUTS_TO_PSBT,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ ADD_OUTPUTS_TO_PSBT error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  async handleExtractTxFromPsbt(callbackId: string, data: any): Promise<void> {
    try {
		console.log("📤 Handling EXTRACT_TX_FROM_PSBT", { callbackId });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.EXTRACT_TX_FROM_PSBT,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ EXTRACT_TX_FROM_PSBT error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  async handleExtractTxFromPsbtSatsNet(callbackId: string, data: any): Promise<void> {
    try {
		console.log("📤 Handling EXTRACT_TX_FROM_PSBT_SATSNET", { callbackId });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.EXTRACT_TX_FROM_PSBT_SATSNET,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ EXTRACT_TX_FROM_PSBT_SATSNET error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理其他工具方法
   */
  async handleGetCurrentName(callbackId: string, data: any): Promise<void> {
    try {
      console.log("📝 Handling GET_CURRENT_NAME", { callbackId, data });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.GET_CURRENT_NAME,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ GET_CURRENT_NAME error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  async handleGetUtxos(callbackId: string, data: any): Promise<void> {
    try {
      console.log("🔍 Handling GET_UTXOS", { callbackId, data });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.GET_UTXOS,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ GET_UTXOS error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  async handleGetUtxosSatsNet(callbackId: string, data: any): Promise<void> {
    try {
      console.log("🔍 Handling GET_UTXOS_SATSNET", { callbackId, data });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.GET_UTXOS_SATSNET,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ GET_UTXOS_SATSNET error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }
}
