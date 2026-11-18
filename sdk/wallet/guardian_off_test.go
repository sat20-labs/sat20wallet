//go:build !guardian

package wallet

// Guardian business cases are selected at compile time, never by environment.
const guardianTests = false
