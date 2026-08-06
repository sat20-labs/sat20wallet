package dkvs

type DKVSWalletRecoveryBackup struct {
	Version         uint32            `json:"version"`
	WalletID        string            `json:"wallet_id"`
	EncryptedBackup []byte            `json:"encrypted_backup"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

type DKVSGuardianShare struct {
	Version    uint32            `json:"version"`
	PackageID  string            `json:"package_id"`
	ShareID    string            `json:"share_id"`
	Ciphertext []byte            `json:"ciphertext"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type DKVSOfflineMessage struct {
	Version          uint32            `json:"version"`
	FromPubKey       []byte            `json:"from_pubkey"`
	ToMailboxID      string            `json:"to_mailbox_id"`
	MessageID        string            `json:"message_id"`
	EncryptedMessage []byte            `json:"encrypted_message"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

type DKVSServiceAuthenticity struct {
	Version      uint32            `json:"version"`
	ServiceName  string            `json:"service_name"`
	AppID        string            `json:"app_id"`
	Release      string            `json:"release,omitempty"`
	ArtifactHash string            `json:"artifact_hash"`
	DownloadURL  string            `json:"download_url,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}
