// SignPsbt Component Contract
// Defines the interface and expected behavior for PSBT signing component

export interface SignPsbtComponentContract {
  // Component Props
  props: {
    psbtHex: string;
    feeRate?: number;
    network?: 'mainnet' | 'testnet';
  };

  // Component Events
  emits: {
    confirm: (signedPsbt: string) => void;
    cancel: () => void;
    error: (error: Error) => void;
  };

  // Required Methods
  methods: {
    onConfirm: () => Promise<void>;
    onCancel: () => void;
    validatePsbt: (psbtHex: string) => boolean;
    signPsbt: (psbtHex: string) => Promise<string>;
  };

  // Expected Behaviors
  behaviors: {
    shouldValidateInput: true;
    shouldHandleErrors: true;
    shouldShowLoadingState: true;
    shouldMaintainTypeSafety: true;
  };
}

// Wallet Store Contract
export interface WalletStoreContract {
  // Required Methods
  actions: {
    signPsbt: (psbtData: string) => Promise<string>;
    validateWallet: () => Promise<boolean>;
    getFeeRate: () => number;
  };

  // State Properties
  state: {
    address: string | null;
    isConnected: boolean;
    feeRate: number;
  };

  // Type Constraints
  constraints: {
    signPsbt: {
      input: 'string (hex)';
      output: 'Promise<string> (hex)';
      errors: ['ValidationError', 'SigningError', 'NetworkError'];
    };
  };
}