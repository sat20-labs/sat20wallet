package wallet

import (
	"fmt"
	indexer "github.com/sat20-labs/indexer/common"
	"strings"
)

// CheckDIDName is the shared preflight for the UI and the actual inscription.
// A missing name is accepted only when the indexer explicitly reports it.
func (p *Manager) CheckDIDName(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !indexer.IsValidSNSName(name) {
		return "invalid", nil
	}
	info, err := p.l1IndexerClient.GetNameInfo(name)
	if err == nil {
		if info != nil {
			return "registered", nil
		}
		return "", fmt.Errorf("name lookup returned no result")
	}
	if err.Error() != fmt.Sprintf("can't find name %s", name) {
		return "", err
	}
	records, err := p.GetOperationLogs()
	if err != nil {
		return "", err
	}
	var latest *OperationLogRecord
	for _, record := range records {
		if record.Action != "inscribe_name" || strings.TrimSpace(record.Parameters["network"]) != p.GetChain() ||
			strings.ToLower(strings.TrimSpace(record.Parameters["name"])) != name {
			continue
		}
		if latest == nil || record.UpdatedAt > latest.UpdatedAt {
			latest = record
		}
	}
	if latest != nil {
		switch latest.Status {
		case OperationLogPending, OperationLogRunning, OperationLogSucceeded:
			return "submitted", nil
		}
	}
	return "available", nil
}
