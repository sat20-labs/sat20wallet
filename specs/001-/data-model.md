# Data Model: Compilation Error Fixes

## Component Entities

### SignPsbt Component
**Purpose**: Vue component for PSBT (Partially Signed Bitcoin Transaction) signing
**Key Properties**:
- `psbtData`: Raw PSBT data to be signed
- `walletStore`: Pinia store instance for wallet operations
- `queryParameters`: URL query parameters (fee rates, amounts)

**Methods**:
- `onConfirm()`: Confirmation handler (corrected from `confirm`)
- `onCancel()`: Cancellation handler (corrected from `cancel`)
- `signPsbt()`: PSBT signing operation (delegated to wallet store)

**State Transitions**:
1. Initialize → Load PSBT data
2. Validate → Check wallet connection and data integrity
3. Sign → Execute signing operation
4. Complete → Return signed PSBT or error

### Wallet Store Integration
**Purpose**: Pinia store managing wallet state and operations
**Key Methods**:
- `signPsbt(psbtData: string): Promise<string>`: Sign PSBT data
- `validateWallet(): Promise<boolean>`: Validate wallet state
- `getFeeRate(): number`: Retrieve current fee rate

**State Properties**:
- `address: string | null`: Current wallet address
- `isConnected: boolean`: Wallet connection status
- `feeRate: number`: Current transaction fee rate

## Type Definitions

### PSBT Data Types
```typescript
interface PsbtData {
  raw: string;
  inputs: PsbtInput[];
  outputs: PsbtOutput[];
  fee: number;
}

interface PsbtInput {
  txid: string;
  vout: number;
  value: number;
}

interface PsbtOutput {
  address: string;
  value: number;
}
```

### Component Props Types
```typescript
interface SignPsbtProps {
  psbtHex: string;
  feeRate?: number;
  network?: 'mainnet' | 'testnet';
}
```

### Query Parameter Types
```typescript
interface QueryParams {
  feeRate?: string | string[];
  amount?: string | string[];
  network?: string | string[];
}
```

## Validation Rules

### PSBT Validation
- PSBT must be valid hex string
- Required inputs must be present
- Outputs must have valid addresses
- Fee must be reasonable amount

### Parameter Validation
- Fee rate must be positive number
- Amount must be positive number
- Network must be supported value

### Type Safety Rules
- All store method calls must be properly typed
- Component props must match interface definitions
- Query parameters must be converted before use
- Variable names must be unique within scope

## Error Handling

### Compilation Error Categories
1. **Property Access Errors**: Use correct method names
2. **Type Mismatch Errors**: Ensure proper type conversion
3. **Missing Method Errors**: Implement or stub missing methods
4. **Variable Scope Errors**: Restructure variable declarations

### Runtime Error Handling
- Validate PSBT data before processing
- Handle wallet connection failures
- Provide meaningful error messages
- Maintain component state consistency