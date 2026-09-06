package xray

import (
	"context"
	"os"
	"testing"

	"github.com/egress-manager/egress-manager/internal/domain"
)

func TestRollbackCommittedRestoresAuthenticatedXraySnapshot(t *testing.T) {
	t.Parallel()
	installation, plan := executableFragmentPlan(t)
	steps := []xrayRunnerStep{
		{name: "xray"}, {name: "systemctl"}, {name: "xray"}, {name: "systemctl"},
		{name: "xray"}, {name: "systemctl"}, {name: "xray"},
		{name: "xray"}, {name: "systemctl"}, {name: "xray"}, {name: "systemctl"},
	}
	runner := &xrayScriptedRunner{t: t, steps: steps}
	store := xrayExecutorStore(t)
	executor := FragmentExecutor{Runner: runner, Journal: store, Protector: xrayExecutorProtector(t), Installation: installation}
	ctx := context.Background()
	if _, err := executor.Execute(ctx, "xray_manual_rollback", plan); err != nil {
		t.Fatal(err)
	}
	operation, err := store.Operation(ctx, "xray_manual_rollback")
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
	if operation.State != domain.TransactionRolledBack || runner.current != len(steps) {
		t.Fatalf("operation=%#v commands=%d/%d", operation, runner.current, len(steps))
	}
	if _, err := os.Stat(installation.ManagedPath); !os.IsNotExist(err) {
		t.Fatalf("Xray fragment was not restored to absence: %v", err)
	}
}
