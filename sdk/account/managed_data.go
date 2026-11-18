package account

// ManagedDataItem carries a module-owned recovery payload in memory.
// Its provider owns the payload format; account management stores modules separately.
type ManagedDataItem struct {
	Provider string
	Scope    string
	Payload  []byte
}

// ManagedDataBundle groups provider payloads in memory. It has no wire format
// or shared revision; each persisted module has its own confirmed record.
type ManagedDataBundle struct {
	Items []ManagedDataItem
}
