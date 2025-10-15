import { Message } from "../../types/message";

export interface InAppBrowserEvent {
  url?: string;
  type?: string;
  data?: any;
  callbackId?: string;
  message?: string;
}

export interface CallbackRecord {
  resolve: (value: any) => void;
  reject: (reason: any) => void;
  type: string;
  timestamp: number;
}

export interface ApprovalMetadata {
  callbackId: string;
  origin: string;
  dAppOrigin: string;
  platform: string;
  url: string;
}

export type InjectionStatus = "idle" | "injecting" | "success" | "failed";

export interface WebViewBridgeState {
  isReady: boolean;
  injectionStatus: InjectionStatus;
  lastError: string | null;
  currentUrl: string;
  isBrowserVisible: boolean;
}

export interface HandlerFunction {
  (callbackId: string, data: any): Promise<void>;
}

export interface MessageHandlerMap {
  [key: string]: HandlerFunction;
}

export interface ProviderConfig {
  inAppBrowserOptions: string;
  timeoutDuration: number;
  version: string;
  platform: string;
}