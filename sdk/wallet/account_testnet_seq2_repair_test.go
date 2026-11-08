//go:build wallet_maintenance
// +build wallet_maintenance

package wallet

// Retired the pre-1.1 one-off testnet seq=2 state/blob repair and its tests.
// Its pinned legacy ciphertext hashes, coupled CAS pair and profile migration
// are not valid maintenance operations for indexed 1.1 modules. No fallback
// repair or old protocol implementation is retained.
