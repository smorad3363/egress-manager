package singbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egress-manager/egress-manager/internal/domain"
)

func TestRollbackCommittedRestoresAuthenticatedSingBoxSnapshot(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	configPath := filepath.Join(directory, "sing-box.json")
	state, _ := ParseState(nil, false)
	plan := executionPlan(t, state, "vless.uri")
	runner := &scriptedRunner{t: t, steps: []runnerStep{
		{name: "sing-box"}, {name: "systemctl"}, {name: "sing-box"}, {name: "systemctl"},
		{name: "sing-box"}, {name: "systemctl"}, {name: "systemctl", contains: []string{"stop " + ownedServiceName}},
	}}
	store := executorStore(t)
	executor := Executor{Runner: runner, Journal: store, Protector: executorProtector(t), ConfigPath: configPath}
	ctx := context.Background()
	if _, err := executor.Execute(ctx, "singbox_manual_rollback", plan); err != nil {
		t.Fatal(err)
	}
	operation, err := store.Operation(ctx, "singbox_manual_rollback")
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.RollbackCommitted(ctx, operation); err != nil {
		t.Fatal(err)
	}
	operation, err = store.Operation(ctx, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if operation.State != domain.TransactionRolledBack || runner.current != len(runner.steps) {
		t.Fatalf("operation=%#v commands=%d/%d", operation, runner.current, len(runner.steps))
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("sing-box snapshot was not restored to absence: %v", err)
	}
}

func TestRollbackCommittedRejectsTamperedSingBoxCandidate(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	state, _ := ParseState(nil, false)
	plan := executionPlan(t, state, "vless.uri")
	runner := &scriptedRunner{t: t, steps: []runnerStep{{name: "sing-box"}, {name: "systemctl"}, {name: "sing-box"}, {name: "systemctl"}}}
	store := executorStore(t)
	executor := Executor{Runner: runner, Journal: store, Protector: executorProtector(t), ConfigPath: filepath.Join(directory, "sing-box.json")}
	ctx := context.Background()
	if _, err := executor.Execute(ctx, "singbox_tampered_rollback", plan); err != nil {
		t.Fatal(err)
	}
	operation, err := store.Operation(ctx, "singbox_tampered_rollback")
	if err != nil {
		t.Fatal(err)
	}
	operation.CandidateConfig += "x"
	if err := executor.RollbackCommitted(ctx, operation); err == nil {
		t.Fatal("RollbackCommitted() accepted a tampered candidate")
	}
	stored, err := store.Operation(ctx, operation.ID)
	if err != nil || stored.State != domain.TransactionCommitted {
		t.Fatalf("stored=%#v error=%v", stored, err)
	}
}
