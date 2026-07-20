package wallet

import (
	"context"
	"errors"
	"fmt"
	"time"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const (
	rgb11AddressMailboxPageSize = 256
	rgb11AddressTemporaryTTL    = uint64((24 * time.Hour) / time.Millisecond)
	rgb11AddressAutopayTTL      = uint64((365 * 24 * time.Hour) / time.Millisecond)
)

type RGB11AddressMailboxSyncResult struct {
	Scanned      int      `json:"scanned"`
	Received     int      `json:"received"`
	ACKs         int      `json:"acks"`
	WaitingTx    int      `json:"waiting_tx"`
	AlreadyDone  int      `json:"already_done"`
	Invalid      int      `json:"invalid"`
	ErrorDetails []string `json:"error_details,omitempty"`
}

func (p *Manager) configuredRGB11DKVSClient() (*SatsNetDKVSClient, error) {
	if p == nil {
		return nil, ErrRGB11Inconsistent
	}
	return p.rgb11DKVSClient()
}

// configureRGB11AddressRetention selects AUTOPAY for an active DKVS tenant and
// otherwise applies a temporary TTL. Failure to query the tenant contract does
// not block an address transfer; it safely falls back to temporary retention,
// which is surfaced by RGB11AddressDeliveryResult.Temporary.
func (p *Manager) configureRGB11AddressRetention(record *dkvsindexer.RecordOptions,
	autopay **DKVSAutopayOptions) {
	if record == nil || autopay == nil {
		return
	}
	if *autopay == nil {
		if paid, err := p.hasActiveRGB11Autopay(); err == nil && paid {
			*autopay = &DKVSAutopayOptions{AddressParams: GetChainParam_SatsNet()}
		}
	}
	if record.TTL == 0 && record.ExpiryHeight == 0 {
		if *autopay != nil {
			record.TTL = rgb11AddressAutopayTTL
		} else {
			record.TTL = rgb11AddressTemporaryTTL
		}
	}
}

func (p *Manager) EnableConfiguredRGB11AddressReceive(options RGB11ReceiveCapabilityOptions) (*RGB11AddressEndpoint, error) {
	client, err := p.configuredRGB11DKVSClient()
	if err != nil {
		return nil, err
	}
	p.configureRGB11AddressRetention(&options.RecordOptions, &options.Autopay)
	return p.EnableRGB11AddressReceive(client, options)
}

func (p *Manager) ResolveConfiguredRGB11AddressEndpoint(address string,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11AddressEndpoint, error) {
	client, err := p.configuredRGB11DKVSClient()
	if err != nil {
		return nil, err
	}
	return p.ResolveRGB11AddressEndpoint(client, address, verify)
}

func (p *Manager) PrepareConfiguredRGB11AddressTransfer(ctx context.Context, request RGB11AddressSendRequest,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11PreparedTransfer, *RGB11AddressEndpoint, error) {
	client, err := p.configuredRGB11DKVSClient()
	if err != nil {
		return nil, nil, err
	}
	return p.PrepareRGB11AddressTransfer(ctx, client, request, verify)
}

func (p *Manager) DeliverAndBroadcastConfiguredRGB11AddressTransfer(transferID string,
	options RGB11AddressDeliveryOptions) (*RGB11AddressDeliveryResult, error) {
	client, err := p.configuredRGB11DKVSClient()
	if err != nil {
		return nil, err
	}
	p.configureRGB11AddressRetention(&options.RecordOptions, &options.Autopay)
	return p.DeliverAndBroadcastRGB11AddressTransfer(client, transferID, options)
}

func rgb11AddressProcessedMetadata(kind, messageID string) string {
	return "address-" + kind + "-" + messageID
}

func (p *Manager) rgb11AddressMessageProcessed(kind, messageID string) bool {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return false
	}
	value, err := p.rgbManager.projectionStore.LoadLocalMetadata(
		rgb11AddressProcessedMetadata(kind, messageID),
	)
	return err == nil && len(value) == 1 && value[0] == 1
}

func (p *Manager) markRGB11AddressMessageProcessed(kind, messageID string) error {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return ErrRGB11Inconsistent
	}
	return p.rgbManager.projectionStore.SaveLocalMetadata(
		rgb11AddressProcessedMetadata(kind, messageID), []byte{1},
	)
}

// SyncConfiguredRGB11AddressMailbox processes the current subaccount mailbox.
// A Consignment whose witness transaction is not visible is intentionally left
// unacknowledged so a later DKVS notify or sync can retry it. Processed cursors
// are device-local cache and are not included in wallet recovery snapshots.
func (p *Manager) SyncConfiguredRGB11AddressMailbox(ctx context.Context,
	verify dkvsindexer.RecordVerificationOptions,
	ackOptions RGB11AddressDeliveryOptions) (*RGB11AddressMailboxSyncResult, error) {
	if p == nil || p.wallet == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return nil, ErrRGB11Inconsistent
	}
	client, err := p.configuredRGB11DKVSClient()
	if err != nil {
		return nil, err
	}
	p.configureRGB11AddressRetention(&ackOptions.RecordOptions, &ackOptions.Autopay)
	accountID, err := dkvsAccountID(p.wallet)
	if err != nil {
		return nil, err
	}
	prefix := "/mail/" + accountID + "/msg"
	if _, err := dkvsindexer.ParsePrefix(prefix); err != nil {
		return nil, err
	}
	if verify.Now == 0 {
		verify.Now = uint64(time.Now().UnixMilli())
	}
	result := &RGB11AddressMailboxSyncResult{}
	for start := 0; ; start += rgb11AddressMailboxPageSize {
		records, total, err := client.ListRecords(prefix, start, rgb11AddressMailboxPageSize)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			result.Scanned++
			_, _, messageID, parseErr := parseRGB11AddressMailboxKey(record)
			if parseErr != nil {
				result.Invalid++
				result.ErrorDetails = append(result.ErrorDetails, parseErr.Error())
				continue
			}
			if len(record.Value) == 4 {
				if p.rgb11AddressMessageProcessed("ack", messageID) {
					result.AlreadyDone++
					continue
				}
				if _, err := p.AcceptRGB11AddressACK(record, verify); err != nil {
					result.Invalid++
					result.ErrorDetails = append(result.ErrorDetails,
						fmt.Sprintf("ack %s: %v", messageID, err))
					continue
				}
				if err := p.markRGB11AddressMessageProcessed("ack", messageID); err != nil {
					return nil, err
				}
				result.ACKs++
				continue
			}
			if p.rgb11AddressMessageProcessed("consignment", messageID) {
				result.AlreadyDone++
				continue
			}
			if _, _, err := p.AcceptRGB11AddressMailbox(ctx, client, record, verify, ackOptions); err != nil {
				if errors.Is(err, ErrRGB11AddressTxNotSeen) {
					result.WaitingTx++
					continue
				}
				result.Invalid++
				result.ErrorDetails = append(result.ErrorDetails,
					fmt.Sprintf("consignment %s: %v", messageID, err))
				continue
			}
			if err := p.markRGB11AddressMessageProcessed("consignment", messageID); err != nil {
				return nil, err
			}
			result.Received++
		}
		if start+len(records) >= total || len(records) == 0 {
			break
		}
	}
	return result, nil
}
