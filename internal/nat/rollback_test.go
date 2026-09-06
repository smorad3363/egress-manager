package nat

import (
	"context"
	"strings"
	"testing"

	"github.com/egress-manager/egress-manager/internal/domain"
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
