package nat

import (
	"encoding/json"
	"fmt"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/secrets"
)

func (executor Executor) protectJournal(id domain.ID, purpose string, plaintext []byte) (string, error) {
	envelope, err := executor.Protector.Seal(natJournalContext(id, purpose), plaintext)
	if err != nil {
		return "", fmt.Errorf("protect NAT journal %s: %w", purpose, err)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("encode protected NAT journal %s: %w", purpose, err)
	}
	return string(encoded), nil
}

func (executor Executor) openJournalField(id domain.ID, purpose, value string, allowLegacy bool) ([]byte, bool, error) {
	var envelope secrets.Envelope
	if err := json.Unmarshal([]byte(value), &envelope); err == nil && len(envelope.Nonce) != 0 && len(envelope.Ciphertext) != 0 {
		if executor.Protector == nil {
			return nil, true, fmt.Errorf("NAT journal protector is required")
		}
		plaintext, err := executor.Protector.Open(natJournalContext(id, purpose), envelope)
		if err != nil {
			return nil, true, fmt.Errorf("authenticate protected NAT journal %s: %w", purpose, err)
		}
		return plaintext, true, nil
	}
	if !allowLegacy {
		return nil, false, fmt.Errorf("NAT operation is not eligible for rollback")
	}
	return []byte(value), false, nil
}

func (executor Executor) openNFTJournal(operation domain.Transaction, allowLegacy bool) (string, string, bool, error) {
	snapshot, snapshotProtected, err := executor.openJournalField(operation.ID, "snapshot", operation.PreviousSnapshot, allowLegacy)
	if err != nil {
		return "", "", false, err
	}
	candidate, candidateProtected, err := executor.openJournalField(operation.ID, "candidate", operation.CandidateConfig, allowLegacy)
	if err != nil {
		return "", "", false, err
	}
	if snapshotProtected != candidateProtected {
		return "", "", false, fmt.Errorf("NAT journal protection is inconsistent")
	}
	candidateFamily, candidateTable, err := ownedTableFromCandidate(string(candidate))
	if err != nil {
		return "", "", false, err
	}
	if string(snapshot) != "absent" {
		family, table, err := ownedTableFromCandidate(string(snapshot))
		if err != nil || family != candidateFamily || table != candidateTable {
			return "", "", false, fmt.Errorf("invalid nftables recovery snapshot")
		}
	}
	return string(snapshot), string(candidate), snapshotProtected, nil
}

func natJournalContext(id domain.ID, purpose string) string {
	return "nat/" + string(id) + "/" + purpose + "/v1"
}
