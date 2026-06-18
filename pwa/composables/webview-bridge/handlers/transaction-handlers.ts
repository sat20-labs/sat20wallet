import { Message } from "../../../types/message";
import { ApprovalHandler } from "../utils/approval-handler";
import { ResponseHandler } from "../utils/response-handler";

export class TransactionHandlers {
  constructor(
    private approvalHandler: ApprovalHandler,
    private responseHandler: ResponseHandler,
    private currentUrl: () => string
  ) {}

  /**
   * 处理 SIGN_MESSAGE - 需要用户授权
   */
  async handleSignMessage(callbackId: string, data: any): Promise<void> {
    try {
      console.log("✍️ Handling SIGN_MESSAGE", { callbackId, data });
      const result = await this.approvalHandler.handleWalletApproval(
        Message.MessageAction.SIGN_MESSAGE,
        data,
        callbackId,
        this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ SIGN_MESSAGE error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 SIGN_DATA - 签原始协议数据，需要用户授权
   */
  async handleSignData(callbackId: string, data: any): Promise<void> {
    try {
      console.log("✍️ Handling SIGN_DATA", { callbackId, data });
      const result = await this.approvalHandler.handleWalletApproval(
        Message.MessageAction.SIGN_DATA,
        { ...data, signData: true },
        callbackId,
        this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ SIGN_DATA error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 SIGN_PSBT - 需要用户授权
   */
  async handleSignPsbt(callbackId: string, data: any): Promise<void> {
    try {
      console.log("📝 Handling SIGN_PSBT", { callbackId, data });
      const result = await this.approvalHandler.handleWalletApproval(
        Message.MessageAction.SIGN_PSBT,
        data,
        callbackId,
        this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ SIGN_PSBT error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 SIGN_PSBTS - 需要用户授权
   */
  async handleSignPsbts(callbackId: string, data: any): Promise<void> {
    try {
      console.log("📝 Handling SIGN_PSBTS", { callbackId, data });
      const result = await this.approvalHandler.handleWalletApproval(
        Message.MessageAction.SIGN_PSBTS,
        data,
        callbackId,
        this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ SIGN_PSBTS error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 SEND_BITCOIN - 需要用户授权
   */
  async handleSendBitcoin(callbackId: string, data: any): Promise<void> {
    try {
      console.log("💸 Handling SEND_BITCOIN", { callbackId, data });
      const result = await this.approvalHandler.handleWalletApproval(
        Message.MessageAction.SEND_BITCOIN,
        data,
        callbackId,
        this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ SEND_BITCOIN error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 SEND_INSCRIPTION - 需要用户授权
   */
  async handleSendInscription(callbackId: string, data: any): Promise<void> {
    try {
      console.log("📤 Handling SEND_INSCRIPTION", { callbackId, data });
      const result = await this.approvalHandler.handleWalletApproval(
        Message.MessageAction.SEND_INSCRIPTION,
        data,
        callbackId,
        this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ SEND_INSCRIPTION error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 PUSH_TX - 直接请求类型
   */
  async handlePushTx(callbackId: string, data: any): Promise<void> {
    try {
      console.log("📤 Handling PUSH_TX", { callbackId, data });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.PUSH_TX,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ PUSH_TX error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 PUSH_PSBT - 直接请求类型
   */
  async handlePushPsbt(callbackId: string, data: any): Promise<void> {
    try {
      console.log("📤 Handling PUSH_PSBT", { callbackId, data });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.PUSH_PSBT,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ PUSH_PSBT error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 GET_INSCRIPTIONS - 直接请求类型
   */
  async handleGetInscriptions(callbackId: string, data: any): Promise<void> {
    try {
      console.log("📜 Handling GET_INSCRIPTIONS", { callbackId, data });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.GET_INSCRIPTIONS,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ GET_INSCRIPTIONS error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }
}
