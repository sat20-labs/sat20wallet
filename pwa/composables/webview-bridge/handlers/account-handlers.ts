import { Message } from "../../../types/message";
import { ApprovalHandler } from "../utils/approval-handler";
import { ResponseHandler } from "../utils/response-handler";
import { LOG_PREFIXES } from "../constants";

export class AccountHandlers {
  constructor(
    private approvalHandler: ApprovalHandler,
    private responseHandler: ResponseHandler,
    private currentUrl: () => string
  ) {}

  /**
   * 处理 REQUEST_ACCOUNTS - 需要用户授权
   */
  async handleRequestAccounts(callbackId: string, data: any): Promise<void> {
    try {
      console.log("🔐 Handling REQUEST_ACCOUNTS", { callbackId, data });
      const result = await this.approvalHandler.handleWalletApproval(
        Message.MessageAction.REQUEST_ACCOUNTS,
        data,
        callbackId,
        this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ REQUEST_ACCOUNTS error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 GET_ACCOUNTS - 直接请求类型（无需用户授权）
   */
  async handleGetAccounts(callbackId: string, data: any): Promise<void> {
    try {
      console.log("📋 Handling GET_ACCOUNTS", { callbackId, data });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.GET_ACCOUNTS,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ GET_ACCOUNTS error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 GET_PUBLIC_KEY
   */
  async handleGetPublicKey(callbackId: string, data: any): Promise<void> {
    try {
      console.log("🔑 Handling GET_PUBLIC_KEY", { callbackId, data });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.GET_PUBLIC_KEY,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ GET_PUBLIC_KEY error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 GET_BALANCE
   */
  async handleGetBalance(callbackId: string, data: any): Promise<void> {
    try {
      console.log("💰 Handling GET_BALANCE", { callbackId, data });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.GET_BALANCE,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ GET_BALANCE error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 SWITCH_NETWORK - 需要用户授权
   */
  async handleSwitchNetwork(callbackId: string, data: any): Promise<void> {
    try {
      console.log("🔄 Handling SWITCH_NETWORK", { callbackId, data });
      const result = await this.approvalHandler.handleWalletApproval(
        Message.MessageAction.SWITCH_NETWORK,
        data,
        callbackId,
        this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ SWITCH_NETWORK error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 GET_NETWORK
   */
  async handleGetNetwork(callbackId: string, data: any): Promise<void> {
    try {
      console.log("🌐 Handling GET_NETWORK", { callbackId, data });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.GET_NETWORK,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ GET_NETWORK error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }
}