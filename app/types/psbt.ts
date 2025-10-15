/**
 * PSBT (Partially Signed Bitcoin Transaction) Type Definitions
 * Defines interfaces for PSBT data structures and component props
 */

import { LocationQueryValue } from 'vue-router'

// PSBT Data Structure
export interface PsbtData {
  raw: string
  inputs: PsbtInput[]
  outputs: PsbtOutput[]
  fee: number
}

// PSBT Input Structure
export interface PsbtInput {
  txid: string
  vout: number
  value: number
  scriptPubKey?: string
  address?: string
}

// PSBT Output Structure
export interface PsbtOutput {
  address: string
  value: number
  scriptPubKey?: string
}

// SignPsbt Component Props
export interface SignPsbtProps {
  psbtHex: string
  feeRate?: number
  network?: 'mainnet' | 'testnet'
}

// SignPsbt Component Emits
export interface SignPsbtEmits {
  (e: 'confirm', signedPsbt: string): void
  (e: 'cancel'): void
  (e: 'error', error: Error): void
}

// URL Query Parameter Types (from Vue Router)
export interface QueryParams {
  psbt?: string | LocationQueryValue[]
  feeRate?: string | LocationQueryValue[]
  amount?: string | LocationQueryValue[]
  network?: string | LocationQueryValue[]
  messageId?: string | LocationQueryValue[]
}

// Component Methods Interface
export interface SignPsbtComponentMethods {
  onConfirm(): Promise<void>
  onCancel(): void
  validatePsbt(psbtHex: string): boolean
  signPsbtTransaction(psbtHex: string): Promise<void>
}

// Wallet Store Method Signatures
export interface WalletStoreMethods {
  signPsbt(psbtData: string): Promise<string>
  validateWallet(): Promise<boolean>
  getFeeRate(): number
}

// PSBT Validation Errors
export class PsbtValidationError extends Error {
  constructor(message: string, public code?: string) {
    super(message)
    this.name = 'PsbtValidationError'
  }
}

// PSBT Signing Errors
export class PsbtSigningError extends Error {
  constructor(message: string, public code?: string) {
    super(message)
    this.name = 'PsbtSigningError'
  }
}

// Component State Interface
export interface SignPsbtComponentState {
  isLoading: boolean
  parseError: boolean
  parsedPsbt: any
  parsedInputs: any[]
  parsedOutputs: any[]
  isWalletConnected: boolean
  feeRate?: number
  amount?: number
}

// Event Payload Types
export interface SignPsbtEventPayload {
  success: {
    signedPsbt: string
    transactionId?: string
  }
  error: {
    message: string
    code?: string
    details?: any
  }
  cancel: {
    reason?: string
  }
}