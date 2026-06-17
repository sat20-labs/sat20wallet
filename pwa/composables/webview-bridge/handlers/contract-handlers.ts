import { Message } from "../../../types/message";
import { ApprovalHandler } from "../utils/approval-handler";
import { ResponseHandler } from "../utils/response-handler";

export class ContractHandlers {
  constructor(
    private approvalHandler: ApprovalHandler,
    private responseHandler: ResponseHandler,
    private currentUrl: () => string
  ) {}

  /**
   * 处理 DEPLOY_CONTRACT_REMOTE - 需要用户授权
   */
  async handleDeployContractRemote(callbackId: string, data: any): Promise<void> {
    try {
      console.log("🚀 Handling DEPLOY_CONTRACT_REMOTE", { callbackId, data });
      const result = await this.approvalHandler.handleWalletApproval(
        Message.MessageAction.DEPLOY_CONTRACT_REMOTE,
        data,
        callbackId,
        this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ DEPLOY_CONTRACT_REMOTE error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 INVOKE_CONTRACT_V2 - 需要用户授权
   */
  async handleInvokeContractV2(callbackId: string, data: any): Promise<void> {
    try {
      console.log("🚀 Handling INVOKE_CONTRACT_V2", { callbackId, data });
      const result = await this.approvalHandler.handleWalletApproval(
        Message.MessageAction.INVOKE_CONTRACT_V2,
        data,
        callbackId,
        this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ INVOKE_CONTRACT_V2 error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 INVOKE_CONTRACT_SATSNET - 需要用户授权
   */
  async handleInvokeContractSatsNet(callbackId: string, data: any): Promise<void> {
    try {
      console.log("⚡ Handling INVOKE_CONTRACT_SATSNET", { callbackId, data });
      const result = await this.approvalHandler.handleWalletApproval(
        Message.MessageAction.INVOKE_CONTRACT_SATSNET,
        data,
        callbackId,
        this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ INVOKE_CONTRACT_SATSNET error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 INVOKE_UNIFIED_CONTRACT - 需要用户授权
   */
  async handleInvokeUnifiedContract(callbackId: string, data: any): Promise<void> {
    try {
      console.log("⚡ Handling INVOKE_UNIFIED_CONTRACT", { callbackId, data });
      const result = await this.approvalHandler.handleWalletApproval(
        Message.MessageAction.INVOKE_UNIFIED_CONTRACT,
        data,
        callbackId,
        this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ INVOKE_UNIFIED_CONTRACT error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 INVOKE_CONTRACT_V2_SATSNET - 需要用户授权
   */
  async handleInvokeContractV2SatsNet(callbackId: string, data: any): Promise<void> {
    try {
      console.log("⚡ Handling INVOKE_CONTRACT_V2_SATSNET", { callbackId, data });
      const result = await this.approvalHandler.handleWalletApproval(
        Message.MessageAction.INVOKE_CONTRACT_V2_SATSNET,
        data,
        callbackId,
        this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ INVOKE_CONTRACT_V2_SATSNET error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理合约相关的直接请求
   */
  async handleGetFeeForDeployContract(callbackId: string, data: any): Promise<void> {
    try {
      console.log("💰 Handling GET_FEE_FOR_DEPLOY_CONTRACT", { callbackId, data });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.GET_FEE_FOR_DEPLOY_CONTRACT,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ GET_FEE_FOR_DEPLOY_CONTRACT error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  async handleGetFeeForInvokeContract(callbackId: string, data: any): Promise<void> {
    try {
      console.log("💰 Handling GET_FEE_FOR_INVOKE_CONTRACT", { callbackId, data });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.GET_FEE_FOR_INVOKE_CONTRACT,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ GET_FEE_FOR_INVOKE_CONTRACT error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  async handleQueryParamForInvokeContract(callbackId: string, data: any): Promise<void> {
    try {
      console.log("🔍 Handling QUERY_PARAM_FOR_INVOKE_CONTRACT", { callbackId, data });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.QUERY_PARAM_FOR_INVOKE_CONTRACT,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ QUERY_PARAM_FOR_INVOKE_CONTRACT error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }
}
