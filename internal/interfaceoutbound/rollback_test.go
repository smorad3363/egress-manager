package interfaceoutbound

import (
	"context"
	"os"
	"testing"

	"github.com/egress-manager/egress-manager/internal/database"
	"github.com/egress-manager/egress-manager/internal/domain"
)

func TestRollbackCommittedRestoresAuthenticatedInterfaceSnapshot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	runner := newLifecycleRunner()
	executor := testLifecycleExecutor(t, root, runner)
	plan, _, _ := lifecyclePlan(t, executor, nil, true)
	ctx := context.Background()
	if _, err := executor.Execute(ctx, "interface_manual_rollback", plan); err != nil {
		t.Fatal(err)
	}
	operation, err := executor.Journal.(*database.Store).Operation(ctx, "interface_manual_rollback")
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.RollbackCommitted(ctx, operation); err != nil {
		t.Fatal(err)
	}
	operation, err = executor.Journal.(*database.Store).Operation(ctx, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if operation.State != domain.TransactionRolledBack || len(runner.links) != 0 || len(runner.services) != 0 {
		t.Fatalf("operation=%#v links=%#v services=%#v", operation, runner.links, runner.services)
	}
	if _, err := os.Stat(executor.StatePath); !os.IsNotExist(err) {
		t.Fatalf("interface state was not restored to absence: %v", err)
	}
}
