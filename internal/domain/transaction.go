package domain

import (
	"fmt"
	"time"
)

type TransactionState string

const (
	TransactionPrepared    TransactionState = "PREPARED"
	TransactionValidated   TransactionState = "VALIDATED"
	TransactionApplying    TransactionState = "APPLYING"
	TransactionVerifying   TransactionState = "VERIFYING"
	TransactionCommitted   TransactionState = "COMMITTED"
	TransactionRollingBack TransactionState = "ROLLING_BACK"
	TransactionRolledBack  TransactionState = "ROLLED_BACK"
	TransactionFailed      TransactionState = "FAILED"
)

var transactionTransitions = map[TransactionState]map[TransactionState]struct{}{
	TransactionPrepared: {
		TransactionValidated:   {},
		TransactionRollingBack: {},
		TransactionFailed:      {},
	},
	TransactionValidated: {
		TransactionApplying:    {},
		TransactionRollingBack: {},
		TransactionFailed:      {},
	},
	TransactionApplying: {
		TransactionVerifying:   {},
		TransactionRollingBack: {},
		TransactionFailed:      {},
	},
	TransactionVerifying: {
		TransactionCommitted:   {},
		TransactionRollingBack: {},
		TransactionFailed:      {},
	},
	TransactionCommitted: {
		TransactionRollingBack: {},
	},
	TransactionRollingBack: {
		TransactionRolledBack: {},
		TransactionFailed:     {},
	},
}

func (state TransactionState) Validate() error {
	switch state {
	case TransactionPrepared, TransactionValidated, TransactionApplying, TransactionVerifying,
		TransactionCommitted, TransactionRollingBack, TransactionRolledBack, TransactionFailed:
		return nil
	default:
		return fmt.Errorf("unsupported transaction state %q", state)
	}
}

func (state TransactionState) CanTransitionTo(next TransactionState) bool {
	_, ok := transactionTransitions[state][next]
	return ok
}

type Transaction struct {
	ID               ID               `json:"id"`
	Operation        string           `json:"operation"`
	State            TransactionState `json:"state"`
	RequestedChange  string           `json:"requested_change"`
	PreviousSnapshot string           `json:"previous_snapshot"`
	CandidateConfig  string           `json:"candidate_config"`
	FailureDetail    string           `json:"failure_detail,omitempty"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
}

func (transaction Transaction) Validate() error {
	errs := []error{
		transaction.ID.Validate("transaction id"),
		validateConfigName("transaction operation", transaction.Operation),
		transaction.State.Validate(),
	}
	if transaction.RequestedChange == "" {
		errs = append(errs, fmt.Errorf("requested change is required"))
	}
	if transaction.PreviousSnapshot == "" {
		errs = append(errs, fmt.Errorf("previous snapshot is required"))
	}
	if transaction.CandidateConfig == "" {
		errs = append(errs, fmt.Errorf("candidate configuration is required"))
	}
	if transaction.CreatedAt.IsZero() || transaction.UpdatedAt.IsZero() {
		errs = append(errs, fmt.Errorf("transaction timestamps are required"))
	} else if transaction.UpdatedAt.Before(transaction.CreatedAt) {
		errs = append(errs, fmt.Errorf("transaction update time precedes creation time"))
	}
	if len(transaction.FailureDetail) > 512 {
		errs = append(errs, fmt.Errorf("failure detail must not exceed 512 bytes"))
	}
	return errorsFrom(errs)
}
