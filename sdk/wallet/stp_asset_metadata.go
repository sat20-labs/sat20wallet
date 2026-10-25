package wallet

// STPAssetMetadataRules supplies the protocol's sat-binding metadata for
// AssetName.N. Quantity precision belongs to the ticker/amount, not identity.
// Legacy protocols retain their existing stored binding metadata.
type STPAssetMetadataRules interface {
	AssetNameN(precision, binding int) int
}

func stpAssetNameN(protocol string, precision, binding int) int {
	if rules, ok := stpAssetProtocols.byName[protocol].(STPAssetMetadataRules); ok {
		return rules.AssetNameN(precision, binding)
	}
	return binding
}
