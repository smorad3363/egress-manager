package nat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/secrets"
	"github.com/egress-manager/egress-manager/internal/system"
)

func TestRollbackCommittedRestoresAuthenticatedNFTSnapshot(t *testing.T) {
	store := testExecutorStore(t)
	plan := executableTestPlan(t)
	runner := &scriptedRunner{t: t, steps: []runnerStep{
		{command: "nft list tables", result: system.Result{ExitCode: 0}},
		{command: "nft --check --file -", result: system.Result{ExitCode: 0}},
		{command: "nft --file -", result: system.Result{ExitCode: 0}},
		{command: "nft list tables", result: system.Result{Stdout: []byte("table ip egm_nat4\n"), ExitCode: 0}},
		{command: "nft list table ip egm_nat4", result: system.Result{Stdout: []byte(plan.Candidate), ExitCode: 0}},
		{command: "nft --check --file -", stdin: "delete table ip egm_nat4", result: system.Result{ExitCode: 0}},
		{command: "nft list tables", result: system.Result{Stdout: []byte("table ip egm_nat4\n"), ExitCode: 0}},
		{command: "nft list table ip egm_nat4", result: system.Result{Stdout: []byte(plan.Candidate), ExitCode: 0}},
		{command: "nft --check --file -", stdin: "delete table ip egm_nat4", result: system.Result{ExitCode: 0}},
		{command: "nft --file -", stdin: "delete table ip egm_nat4", result: system.Result{ExitCode: 0}},
	}}
	executor := Executor{Runner: runner, Journal: store, Verifier: &fakeVerifier{}, Protector: natTestProtector(t)}
	if err := executor.Execute(context.Background(), "nft_manual_rollback", `{}`, plan); err != nil {
		t.Fatal(err)
	}
	operation, _ := store.Operation(context.Background(), "nft_manual_rollback")
	if strings.Contains(operation.CandidateConfig, "egm_nat4") {
		t.Fatal("NAT journal exposed plaintext candidate")
	}
	if err := executor.RollbackCommitted(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	operation, _ = store.Operation(context.Background(), operation.ID)
	if operation.State != domain.TransactionRolledBack {
		t.Fatalf("operation state = %s", operation.State)
	}
	runner.assertDone()
}

func TestRollbackCommittedRestoresAuthenticatedIPTablesSnapshot(t *testing.T) {
	store := testExecutorStore(t)
	plan := executableIPTablesPlan(t)
	parts := strings.SplitN(plan.Candidate, "*filter\n", 2)
	currentNAT := strings.ReplaceAll(parts[0], "-I PREROUTING 1 ", "-A PREROUTING ")
	currentNAT = strings.ReplaceAll(currentNAT, "-I POSTROUTING 1 ", "-A POSTROUTING ")
	currentFilter := "*filter\n" + strings.ReplaceAll(parts[1], "-I FORWARD 1 ", "-A FORWARD ")
	steps := []runnerStep{
		{command: "iptables-save -t nat", result: system.Result{Stdout: []byte(emptyIPTablesNAT), ExitCode: 0}},
		{command: "iptables-save -t filter", result: system.Result{Stdout: []byte(emptyIPTablesFilter), ExitCode: 0}},
		{command: "iptables-restore --test --noflush", result: system.Result{ExitCode: 0}},
		{command: "iptables-restore --noflush", result: system.Result{ExitCode: 0}},
		{command: "iptables -t nat -C PREROUTING -m comment --comment egm_anchor_prerouting -j EGM_PREROUTING", result: system.Result{ExitCode: 0}},
		{command: "iptables -t nat -C POSTROUTING -m comment --comment egm_anchor_postrouting -j EGM_POSTROUTING", result: system.Result{ExitCode: 0}},
		{command: "iptables -t filter -C FORWARD -m comment --comment egm_anchor_forward -j EGM_FORWARD", result: system.Result{ExitCode: 0}},
		{command: "ip route get 10.10.0.5", result: system.Result{ExitCode: 0}},
		{command: "iptables-save -t nat", result: system.Result{Stdout: []byte(currentNAT), ExitCode: 0}},
		{command: "iptables-save -t filter", result: system.Result{Stdout: []byte(currentFilter), ExitCode: 0}},
		{command: "iptables-restore --test --noflush", result: system.Result{ExitCode: 0}},
		{command: "iptables-save -t nat", result: system.Result{Stdout: []byte(currentNAT), ExitCode: 0}},
		{command: "iptables-save -t filter", result: system.Result{Stdout: []byte(currentFilter), ExitCode: 0}},
		{command: "iptables-restore --test --noflush", result: system.Result{ExitCode: 0}},
		{command: "iptables-restore --noflush", result: system.Result{ExitCode: 0}},
	}
	executor := Executor{Runner: &scriptedRunner{t: t, steps: steps}, Journal: store, Protector: natTestProtector(t)}
	if err := executor.Execute(context.Background(), "iptables_manual_rollback", `{}`, plan); err != nil {
		t.Fatal(err)
	}
	operation, _ := store.Operation(context.Background(), "iptables_manual_rollback")
	if err := executor.RollbackCommitted(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	operation, _ = store.Operation(context.Background(), operation.ID)
	if operation.State != domain.TransactionRolledBack {
		t.Fatalf("operation state = %s", operation.State)
	}
}

func TestRollbackCommittedRejectsLegacyPlaintextNFTJournal(t *testing.T) {
	plan := executableTestPlan(t)
	operation := domain.Transaction{ID: "legacy_nft", Operation: "nat_apply", State: domain.TransactionCommitted, PreviousSnapshot: "absent", CandidateConfig: plan.Candidate}
	executor := Executor{Runner: &scriptedRunner{t: t}, Journal: testExecutorStore(t), Protector: natTestProtector(t)}
	if err := executor.RollbackCommitted(context.Background(), operation); err == nil {
		t.Fatal("RollbackCommitted() accepted a legacy plaintext journal")
	}
}

func TestIPTablesCandidateStatePreservesExistingAnchors(t *testing.T) {
	previous := IPTablesState{PreroutingChain: true, PostroutingChain: true, ForwardChain: true, PreroutingAnchor: true, PostroutingAnchor: true, ForwardAnchor: true, NatRules: []string{}, FilterRules: []string{}}
	forward := testForward()
	forward.RemotePortStart = 0
	plan, err := BuildIPTablesPlan(IPv4, []domain.PortForward{forward}, nil, testPolicy(), previous)
	if err != nil {
		t.Fatal(err)
	}
	state, err := iptablesStateFromCandidate([]byte(plan.Candidate), previous)
	if err != nil {
		t.Fatal(err)
	}
	if !state.PreroutingAnchor || !state.PostroutingAnchor || !state.ForwardAnchor {
		t.Fatalf("candidate state lost existing anchors: %#v", state)
	}
}

func TestRollbackCommittedRejectsMissingNFTStateBeforeTransition(t *testing.T) {
	store := testExecutorStore(t)
	plan := executableTestPlan(t)
	runner := &scriptedRunner{t: t, steps: []runnerStep{
		{command: "nft list tables", result: system.Result{ExitCode: 0}},
		{command: "nft --check --file -", result: system.Result{ExitCode: 0}},
		{command: "nft --file -", result: system.Result{ExitCode: 0}},
		{command: "nft list tables", result: system.Result{ExitCode: 0}},
	}}
	executor := Executor{Runner: runner, Journal: store, Verifier: &fakeVerifier{}, Protector: natTestProtector(t)}
	if err := executor.Execute(context.Background(), "nft_runtime_lost", `{}`, plan); err != nil {
		t.Fatal(err)
	}
	operation, _ := store.Operation(context.Background(), "nft_runtime_lost")
	if err := executor.RollbackCommitted(context.Background(), operation); !errors.Is(err, ErrHostStateChanged) {
		t.Fatalf("RollbackCommitted() error = %v", err)
	}
	operation, _ = store.Operation(context.Background(), operation.ID)
	if operation.State != domain.TransactionCommitted {
		t.Fatalf("operation state = %s", operation.State)
	}
	runner.assertDone()
}

func TestRecoveryLeavesTamperedAuthenticatedNATJournalUnfinished(t *testing.T) {
	for _, operationType := range []string{"nat_apply", "nat_apply_iptables"} {
		t.Run(operationType, func(t *testing.T) {
			store := testExecutorStore(t)
			executor := Executor{Runner: &scriptedRunner{t: t}, Journal: store, Protector: natTestProtector(t)}
			id := domain.ID("tampered_" + operationType)
			candidate := executableTestPlan(t).Candidate
			snapshot := []byte("absent")
			if operationType == "nat_apply_iptables" {
				candidate = executableIPTablesPlan(t).Candidate
				snapshot, _ = json.Marshal(iptablesRecoverySnapshot{Family: IPv4, State: IPTablesState{NatRules: []string{}, FilterRules: []string{}}})
			}
			protectedSnapshot, err := executor.protectJournal(id, "snapshot", snapshot)
			if err != nil {
				t.Fatal(err)
			}
			protectedCandidate, err := executor.protectJournal(id, "candidate", []byte(candidate))
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			operation := domain.Transaction{ID: id, Operation: operationType, State: domain.TransactionPrepared, RequestedChange: `{}`, PreviousSnapshot: tamperNATEnvelope(t, protectedSnapshot), CandidateConfig: protectedCandidate, CreatedAt: now, UpdatedAt: now}
			if err := store.CreateOperation(context.Background(), operation); err != nil {
				t.Fatal(err)
			}
			if err := store.TransitionOperation(context.Background(), id, domain.TransactionPrepared, domain.TransactionValidated, now, ""); err != nil {
				t.Fatal(err)
			}
			if err := store.TransitionOperation(context.Background(), id, domain.TransactionValidated, domain.TransactionApplying, now, ""); err != nil {
				t.Fatal(err)
			}
			if err := executor.Recover(context.Background()); err == nil {
				t.Fatal("Recover() accepted a tampered authenticated journal")
			}
			operation, _ = store.Operation(context.Background(), id)
			if operation.State != domain.TransactionApplying {
				t.Fatalf("tampered operation state = %s", operation.State)
			}
		})
	}
}

func tamperNATEnvelope(t *testing.T, value string) string {
	t.Helper()
	var envelope secrets.Envelope
	if err := json.Unmarshal([]byte(value), &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Ciphertext[len(envelope.Ciphertext)-1] ^= 1
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
