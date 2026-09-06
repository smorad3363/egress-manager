package nat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/system"
)

const emptyIPTablesNAT = "*nat\n:PREROUTING ACCEPT [0:0]\n:POSTROUTING ACCEPT [0:0]\n:FOREIGN_LAB - [0:0]\n-A FOREIGN_LAB -m comment --comment \"foreign_marker\" -j RETURN\nCOMMIT\n"
const emptyIPTablesFilter = "*filter\n:FORWARD ACCEPT [0:0]\nCOMMIT\n"

func executableIPTablesPlan(t *testing.T) Plan {
	t.Helper()
	forward := testForward()
	forward.RemotePortStart = 0
	plan, err := BuildIPTablesPlan(IPv4, []domain.PortForward{forward}, nil, testPolicy(), IPTablesState{NatRules: []string{}, FilterRules: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestIPTablesExecutorCommitsValidatedVerifiedPlan(t *testing.T) {
	t.Parallel()
	store := testExecutorStore(t)
	plan := executableIPTablesPlan(t)
	runner := &scriptedRunner{t: t, steps: []runnerStep{
		{command: "iptables-save -t nat", result: system.Result{Stdout: []byte(emptyIPTablesNAT), ExitCode: 0}},
		{command: "iptables-save -t filter", result: system.Result{Stdout: []byte(emptyIPTablesFilter), ExitCode: 0}},
		{command: "iptables-restore --test --noflush", stdin: "EGM_PREROUTING", result: system.Result{ExitCode: 0}},
		{command: "iptables-restore --noflush", stdin: "EGM_FORWARD", result: system.Result{ExitCode: 0}},
		{command: "iptables -t nat -C PREROUTING -m comment --comment egm_anchor_prerouting -j EGM_PREROUTING", result: system.Result{ExitCode: 0}},
		{command: "iptables -t nat -C POSTROUTING -m comment --comment egm_anchor_postrouting -j EGM_POSTROUTING", result: system.Result{ExitCode: 0}},
		{command: "iptables -t filter -C FORWARD -m comment --comment egm_anchor_forward -j EGM_FORWARD", result: system.Result{ExitCode: 0}},
		{command: "ip route get 10.10.0.5", result: system.Result{ExitCode: 0}},
	}}
	executor := Executor{Runner: runner, Journal: store, Protector: natTestProtector(t)}
	if err := executor.Execute(context.Background(), "iptables_success", `{}`, plan); err != nil {
		t.Fatal(err)
	}
	operation, err := store.Operation(context.Background(), "iptables_success")
	if err != nil {
		t.Fatal(err)
	}
	if operation.State != domain.TransactionCommitted || operation.Operation != "nat_apply_iptables" {
		t.Fatalf("operation = %#v", operation)
	}
	runner.assertDone()
}

func TestIPTablesExecutorRollsBackOnlyOwnedChains(t *testing.T) {
	t.Parallel()
	store := testExecutorStore(t)
	plan := executableIPTablesPlan(t)
	currentNAT := `*nat
:PREROUTING ACCEPT [0:0]
:POSTROUTING ACCEPT [0:0]
:FOREIGN_LAB - [0:0]
:EGM_PREROUTING - [0:0]
:EGM_POSTROUTING - [0:0]
-A PREROUTING -m comment --comment "egm_anchor_prerouting" -j EGM_PREROUTING
-A POSTROUTING -m comment --comment "egm_anchor_postrouting" -j EGM_POSTROUTING
-A FOREIGN_LAB -m comment --comment "foreign_marker" -j RETURN
-A EGM_PREROUTING -m comment --comment "egm_pf_web_forward_dnat" -j RETURN
-A EGM_POSTROUTING -m comment --comment "egm_pf_web_forward_masquerade" -j RETURN
COMMIT
`
	currentFilter := `*filter
:FORWARD ACCEPT [0:0]
:EGM_FORWARD - [0:0]
-A FORWARD -m comment --comment "egm_anchor_forward" -j EGM_FORWARD
-A EGM_FORWARD -m comment --comment "egm_established" -j ACCEPT
COMMIT
`
	runner := &scriptedRunner{t: t, steps: []runnerStep{
		{command: "iptables-save -t nat", result: system.Result{Stdout: []byte(emptyIPTablesNAT), ExitCode: 0}},
		{command: "iptables-save -t filter", result: system.Result{Stdout: []byte(emptyIPTablesFilter), ExitCode: 0}},
		{command: "iptables-restore --test --noflush", result: system.Result{ExitCode: 0}},
		{command: "iptables-restore --noflush", result: system.Result{ExitCode: 0}},
		{command: "iptables -t nat -C PREROUTING -m comment --comment egm_anchor_prerouting -j EGM_PREROUTING", result: system.Result{ExitCode: 1}, err: errors.New("missing anchor")},
		{command: "iptables-save -t nat", result: system.Result{Stdout: []byte(currentNAT), ExitCode: 0}},
		{command: "iptables-save -t filter", result: system.Result{Stdout: []byte(currentFilter), ExitCode: 0}},
		{command: "iptables-restore --test --noflush", stdin: "-X EGM_PREROUTING", result: system.Result{ExitCode: 0}},
		{command: "iptables-restore --noflush", stdin: "-D FORWARD", result: system.Result{ExitCode: 0}},
	}}
	executor := Executor{Runner: runner, Journal: store, Protector: natTestProtector(t)}
	if err := executor.Execute(context.Background(), "iptables_rollback", `{}`, plan); err == nil || !strings.Contains(err.Error(), "missing anchor") {
		t.Fatalf("Execute() error = %v", err)
	}
	operation, err := store.Operation(context.Background(), "iptables_rollback")
	if err != nil {
		t.Fatal(err)
	}
	if operation.State != domain.TransactionRolledBack {
		t.Fatalf("operation = %#v", operation)
	}
	runner.assertDone()
}

func TestParseIPTablesCountersAggregatesAllowAndDrop(t *testing.T) {
	t.Parallel()
	input := `[4:320] -A EGM_FORWARD -m comment --comment "egm_pf_web_tls_allow" -j ACCEPT
[2:128] -A EGM_FORWARD -m comment --comment "egm_pf_web_tls_allow" -j ACCEPT
[1:64] -A EGM_FORWARD -m comment --comment "egm_pf_web_tls_source_drop" -j DROP
[999:999] -A FOREIGN_LAB -m comment --comment "egm_pf_web_tls_allow" -j ACCEPT
`
	items, err := parseIPTablesCounters([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ForwardID != "web_tls" || items[0].AcceptedPackets != 6 || items[0].AcceptedBytes != 448 || items[0].DroppedPackets != 1 {
		t.Fatalf("items = %#v", items)
	}
}

func TestIPTablesPlanRejectsStaleOwnedState(t *testing.T) {
	t.Parallel()
	store := testExecutorStore(t)
	plan := executableIPTablesPlan(t)
	changedNAT := strings.Replace(emptyIPTablesNAT, "COMMIT", ":EGM_PREROUTING - [0:0]\nCOMMIT", 1)
	runner := &scriptedRunner{t: t, steps: []runnerStep{
		{command: "iptables-save -t nat", result: system.Result{Stdout: []byte(changedNAT), ExitCode: 0}},
		{command: "iptables-save -t filter", result: system.Result{Stdout: []byte(emptyIPTablesFilter), ExitCode: 0}},
	}}
	if err := (Executor{Runner: runner, Journal: store, Protector: natTestProtector(t)}).Execute(context.Background(), "iptables_stale", `{}`, plan); !errors.Is(err, ErrHostStateChanged) {
		t.Fatalf("Execute() error = %v", err)
	}
	runner.assertDone()
}

func TestIPTablesRecoveryAcceptsLegacyPlaintextSnapshot(t *testing.T) {
	store := testExecutorStore(t)
	snapshot, err := json.Marshal(iptablesRecoverySnapshot{Family: IPv4, State: IPTablesState{NatRules: []string{}, FilterRules: []string{}}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	operation := domain.Transaction{ID: "iptables_legacy_recovery", Operation: "nat_apply_iptables", State: domain.TransactionPrepared, RequestedChange: `{}`, PreviousSnapshot: string(snapshot), CandidateConfig: executableIPTablesPlan(t).Candidate, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateOperation(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	executor := Executor{Runner: &scriptedRunner{t: t}, Journal: store}
	if err := executor.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	operation, _ = store.Operation(context.Background(), operation.ID)
	if operation.State != domain.TransactionFailed || operation.FailureDetail != "interrupted_before_apply" {
		t.Fatalf("operation = %#v", operation)
	}
}
