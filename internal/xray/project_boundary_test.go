package xray

import (
	"context"
	"testing"

	"github.com/egress-manager/egress-manager/internal/system"
)

type noTouchFiles struct{ t *testing.T }

func (files noTouchFiles) Inspect(path string) (FileMetadata, error) {
	files.t.Fatalf("production Xray discovery inspected foreign path %q", path)
	return FileMetadata{}, nil
}

func (files noTouchFiles) ReadFile(path string) ([]byte, error) {
	files.t.Fatalf("production Xray discovery read foreign path %q", path)
	return nil, nil
}

type noTouchRunner struct{ t *testing.T }

func (runner noTouchRunner) Run(_ context.Context, command system.Command) (system.Result, error) {
	runner.t.Fatalf("production Xray discovery executed foreign probe %#v", command)
	return system.Result{}, nil
}

func TestProjectBoundaryDoesNotInspectForeignXray(t *testing.T) {
	if len(knownCandidates) != 0 {
		t.Fatalf("production Xray candidate catalog must be empty, got %d entries", len(knownCandidates))
	}
	report, err := (Discoverer{Runner: noTouchRunner{t: t}, Files: noTouchFiles{t: t}}).Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Installations) != 0 || len(report.Warnings) != 0 {
		t.Fatalf("report = %#v", report)
	}
}
