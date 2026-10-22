package wallet

// STPAssetMetadataRules supplies the protocol's meaning of the existing
// AssetName.N field. Legacy protocols keep their stored sat-binding metadata;
// native protocols may identify quantity precision separately from binding.
// Reading old ticker records therefore does not require rewriting their N.
type STPAssetMetadataRules interface {
	AssetNameN(precision, binding int) int
}

func stpAssetNameN(protocol string, precision, binding int) int {
	if rules, ok := stpAssetProtocols.byName[protocol].(STPAssetMetadataRules); ok {
		return rules.AssetNameN(precision, binding)
	}
	return binding
}
