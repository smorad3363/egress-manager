package reliability

import (
	"context"
	"fmt"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
)

const MaximumRecoveryOperations = 1000

type Journal interface {
	UnfinishedOperations(context.Context, int) ([]domain.Transaction, error)
}

type OperationSummary struct {
	ID        domain.ID               `json:"id"`
	Operation string                  `json:"operation"`
	State     domain.TransactionState `json:"state"`
	CreatedAt time.Time               `json:"created_at"`
	UpdatedAt time.Time               `json:"updated_at"`
}

type RecoveryStatus struct {
	LastRecovery     *RecoveryReport    `json:"last_recovery,omitempty"`
	Ready            bool               `json:"ready"`
	RecoveryRequired bool               `json:"recovery_required"`
	MutationLock     Status             `json:"mutation_lock"`
	Unfinished       []OperationSummary `json:"unfinished_operations"`
	Dependencies     []DependencyStatus `json:"dependencies"`
	ObservedAt       time.Time          `json:"observed_at"`
}

func Inspect(ctx context.Context, journal Journal, lock FileLock, now time.Time) (RecoveryStatus, error) {
	if ctx == nil || journal == nil {
		return RecoveryStatus{}, fmt.Errorf("recovery status dependencies are required")
	}
	if now.IsZero() {
		return RecoveryStatus{}, fmt.Errorf("recovery status observation time is required")
	}
	lockStatus, err := lock.Inspect()
	if err != nil {
		return RecoveryStatus{}, err
	}
	unfinished, err := journal.UnfinishedOperations(ctx, MaximumRecoveryOperations)
	if err != nil {
		return RecoveryStatus{}, fmt.Errorf("read unfinished recovery operations: %w", err)
	}
	summaries := make([]OperationSummary, 0, len(unfinished))
	for _, operation := range unfinished {
		summaries = append(summaries, OperationSummary{
			ID:        operation.ID,
			Operation: operation.Operation,
			State:     operation.State,
			CreatedAt: operation.CreatedAt,
			UpdatedAt: operation.UpdatedAt,
		})
	}
	recoveryRequired := len(summaries) > 0
	return RecoveryStatus{
		Ready:            !recoveryRequired,
		RecoveryRequired: recoveryRequired,
		MutationLock:     lockStatus,
		Unfinished:       summaries,
		Dependencies:     []DependencyStatus{},
		ObservedAt:       now.UTC(),
	}, nil
}
