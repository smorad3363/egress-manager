package domain

import (
	"testing"
	"time"
)

func TestTransactionTransitions(t *testing.T) {
	t.Parallel()

	valid := []TransactionState{
		TransactionPrepared,
		TransactionValidated,
		TransactionApplying,
		TransactionVerifying,
		TransactionCommitted,
	}
	for index := 0; index < len(valid)-1; index++ {
		if !valid[index].CanTransitionTo(valid[index+1]) {
			t.Fatalf("expected transition %s -> %s", valid[index], valid[index+1])
		}
	}
	if TransactionCommitted.CanTransitionTo(TransactionApplying) {
		t.Fatal("committed transaction must not be reapplied")
	}
	if !TransactionCommitted.CanTransitionTo(TransactionRollingBack) {
		t.Fatal("committed transaction must permit explicit rollback")
	}
	if !TransactionApplying.CanTransitionTo(TransactionRollingBack) {
		t.Fatal("applying transaction must permit rollback")
	}
}

func TestTransactionValidation(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	transaction := Transaction{
		ID:               "op-20260904-1",
		Operation:        "apply_port_forward",
		State:            TransactionPrepared,
		RequestedChange:  `{"id":"web-forward"}`,
		PreviousSnapshot: `{"table":"egm"}`,
		CandidateConfig:  "table inet egm {}",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := transaction.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	transaction.PreviousSnapshot = ""
	if err := transaction.Validate(); err == nil {
		t.Fatal("Validate() accepted a transaction without rollback snapshot")
	}
}
