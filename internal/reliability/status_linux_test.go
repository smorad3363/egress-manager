//go:build linux

package reliability

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
)

type statusJournal struct {
	operations []domain.Transaction
	limit      int
}

func (journal *statusJournal) UnfinishedOperations(_ context.Context, limit int) ([]domain.Transaction, error) {
	journal.limit = limit
	return journal.operations, nil
}

func TestInspectReturnsSecretFreeRecoveryStatusWhileMutationIsActive(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 3, 0, 0, 0, time.UTC)
	lock := FileLock{Path: filepath.Join(t.TempDir(), "operation.lock"), Now: func() time.Time { return now }, PID: 42, ProcessStart: "test-boot:123"}
	lease, err := lock.TryAcquire("route_apply", "routing")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	journal := &statusJournal{operations: []domain.Transaction{{
		ID: "interrupted", Operation: "route_engine_apply", State: domain.TransactionApplying,
		PreviousSnapshot: "TEST_ONLY_SECRET_SNAPSHOT", CandidateConfig: "TEST_ONLY_SECRET_CANDIDATE",
		CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
	}}}
	status, err := Inspect(context.Background(), journal, lock, now)
	if err != nil {
		t.Fatal(err)
	}
	if status.Ready || !status.RecoveryRequired || !status.MutationLock.Active || status.MutationLock.Owner == nil || len(status.Unfinished) != 1 || status.Dependencies == nil || journal.limit != MaximumRecoveryOperations {
		t.Fatalf("recovery status = %#v", status)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "TEST_ONLY_SECRET") {
		t.Fatal("recovery status exposed protected journal content")
	}
}
