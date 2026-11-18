package common

// UTXO lock reasons identify the existing source of protection. Keep the stored
// values unchanged: these constants do not alter ownership or release policy.
const (
	UtxoLockManual          = "manual"
	UtxoLockBroadcasted     = "broadcasted"
	UtxoLockRGB             = "rgb"
	UtxoLockRGBPending      = "pending-rgb"
	UtxoLockContractDeposit = "contract deposit"
	UtxoLockRGBWithdrawal   = "rgb11-contract-withdrawal"
	UtxoLockClosedTopup     = "closed-channel-topup"
	UtxoLockStubPrefix      = "stub for "
)
