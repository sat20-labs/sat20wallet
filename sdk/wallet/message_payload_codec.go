package wallet

import (
	"bytes"
	"fmt"
	"strings"

	swire "github.com/sat20-labs/satoshinet/wire"
)

const accountMessagePayloadMagic = "AMSG"

// The plaintext is encoded before encryption and signing. HTTP envelopes keep
// their JSON representation; the encrypted DKVS value contains no JSON/base64.
func encodeAccountMessagePayload(kind, applicationID string, body []byte) ([]byte, error) {
	kind = strings.TrimSpace(kind)
	if kind == "" || !validMessageApplicationID(applicationID) || len(body) == 0 ||
		len(kind) > swire.MaxDirectMessageCiphertextSize || len(applicationID) > swire.MaxDirectMessageCiphertextSize || len(body) > swire.MaxDirectMessageCiphertextSize {
		return nil, fmt.Errorf("invalid account message payload")
	}
	var buf bytes.Buffer
	buf.WriteString(accountMessagePayloadMagic)
	buf.WriteByte(byte(AccountMessagePayloadVersion))
	for _, field := range [][]byte{[]byte(kind), []byte(applicationID), body} {
		if err := swire.WriteVarBytes(&buf, 0, field); err != nil {
			return nil, err
		}
	}
	if buf.Len() > swire.MaxDirectMessageCiphertextSize {
		return nil, fmt.Errorf("invalid account message payload")
	}
	return buf.Bytes(), nil
}

func decodeAccountMessagePayload(encoded []byte) (*AccountMessagePayload, error) {
	invalid := fmt.Errorf("invalid account message payload")
	if len(encoded) < len(accountMessagePayloadMagic)+1 || len(encoded) > swire.MaxDirectMessageCiphertextSize ||
		string(encoded[:len(accountMessagePayloadMagic)]) != accountMessagePayloadMagic ||
		uint32(encoded[len(accountMessagePayloadMagic)]) != AccountMessagePayloadVersion {
		return nil, invalid
	}
	r := bytes.NewReader(encoded[len(accountMessagePayloadMagic)+1:])
	fields := make([][]byte, 3)
	for n := range fields {
		field, err := swire.ReadVarBytes(r, 0, uint32(r.Len()), "account message payload")
		if err != nil {
			return nil, invalid
		}
		fields[n] = field
	}
	payload := &AccountMessagePayload{
		Version: AccountMessagePayloadVersion, Kind: string(fields[0]),
		ApplicationID: string(fields[1]), Body: fields[2],
	}
	if r.Len() != 0 || strings.TrimSpace(payload.Kind) == "" || !validMessageApplicationID(payload.ApplicationID) || len(payload.Body) == 0 {
		return nil, invalid
	}
	// CompactSize lengths must be canonical, and the stored kind must match
	// the normalized value used by the sender.
	canonical, err := encodeAccountMessagePayload(payload.Kind, payload.ApplicationID, payload.Body)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return nil, invalid
	}
	return payload, nil
}
