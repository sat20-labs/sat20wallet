package wallet

import "testing"

// Optional evidence adapter for the fixed MCP profile. The business tests
// themselves are ordinary, untagged sdk/e2e tests and run in go test ./....
// No alternate module file, compiler selection, or application build is used.
func TestSmartContractReleaseGateValidation(t *testing.T) {
	runSmartContractReleaseFixValidation(t, false)
}
