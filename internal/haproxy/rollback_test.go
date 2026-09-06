package haproxy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egress-manager/egress-manager/internal/domain"
)

func TestRollbackCommittedRemovesAuthenticatedInitialHAProxyApply(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "haproxy.cfg")
	pidPath := filepath.Join(directory, "haproxy.pid")
	store := haproxyTestStore(t)
	runner := &haproxyRunner{t: t, steps: []haproxyRunnerStep{
		{name: "haproxy", contains: []string{"-c -f "}},
		{name: "haproxy", contains: []string{"-D -W", "-f " + configPath}},
	}}
	executor := Executor{Runner: runner, Journal: store, Runtime: &switchRuntime{}, Protector: haproxyTestProtector(t), ConfigPath: configPath, PIDPath: pidPath}
	plan := executorTestPlan(t, nil, false)
	if _, err := executor.Execute(context.Background(), "haproxy_manual_rollback", `{}`, plan); err != nil {
		t.Fatal(err)
	}
	operation, err := store.Operation(context.Background(), "haproxy_manual_rollback")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(operation.PreviousSnapshot, plan.Candidate) || strings.Contains(operation.CandidateConfig, plan.Candidate) {
		t.Fatal("HAProxy journal exposed plaintext configuration")
	}
	if err := executor.RollbackCommitted(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("rolled-back initial configuration still exists: %v", err)
	}
	operation, _ = store.Operation(context.Background(), operation.ID)
	if operation.State != domain.TransactionRolledBack {
		t.Fatalf("operation state = %s", operation.State)
	}
}

func TestRollbackCommittedRejectsTamperedHAProxyCandidate(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "haproxy.cfg")
	store := haproxyTestStore(t)
	executor := Executor{Runner: &haproxyRunner{t: t, steps: []haproxyRunnerStep{{name: "haproxy"}, {name: "haproxy"}}}, Journal: store, Runtime: &switchRuntime{}, Protector: haproxyTestProtector(t), ConfigPath: configPath, PIDPath: filepath.Join(directory, "haproxy.pid")}
	if _, err := executor.Execute(context.Background(), "haproxy_tampered", `{}`, executorTestPlan(t, nil, false)); err != nil {
		t.Fatal(err)
	}
	operation, _ := store.Operation(context.Background(), "haproxy_tampered")
	operation.CandidateConfig = operation.CandidateConfig[:len(operation.CandidateConfig)-2] + "xx"
	if err := executor.RollbackCommitted(context.Background(), operation); err == nil {
		t.Fatal("RollbackCommitted() accepted a tampered candidate")
	}
	loaded, _ := store.Operation(context.Background(), operation.ID)
	if loaded.State != domain.TransactionCommitted {
		t.Fatalf("operation state = %s", loaded.State)
	}
}
