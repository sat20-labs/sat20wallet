package dkvs

type DKVSOfflineMessage struct {
	Version          uint32            `json:"version"`
	FromPubKey       []byte            `json:"from_pubkey"`
	ToMailboxID      string            `json:"to_mailbox_id"`
	MessageID        int64             `json:"message_id"`
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
