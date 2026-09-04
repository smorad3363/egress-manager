package app

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/config"
	"github.com/egress-manager/egress-manager/internal/database"
	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/ipc"
	"github.com/egress-manager/egress-manager/internal/reliability"
)

func TestFailedStartupRecoveryKeepsEmergencyIPCAndBlocksApply(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux Unix socket integration")
	}
	directory := t.TempDir()
	configuration := config.Default(directory)
	configuration.ListenPort = 18080
	configPath := filepath.Join(directory, "config.json")
	if err := config.Save(configPath, configuration); err != nil {
		t.Fatal(err)
	}
	encoded, err := config.GenerateSharedKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(directory, "ipc.key")
	if err := os.WriteFile(keyPath, []byte(encoded+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	connection, err := database.Open(context.Background(), configuration.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	store := database.NewStore(connection)
	now := time.Now().UTC()
	if err := store.CreateOperation(context.Background(), domain.Transaction{ID: "unknown_recovery", Operation: "unknown_component", State: domain.TransactionPrepared, RequestedChange: "test", PreviousSnapshot: "{}", CandidateConfig: "{}", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- RunDaemon(ctx, DaemonOptions{ConfigPath: configPath, KeyPath: keyPath, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-result:
			if err != nil {
				t.Errorf("daemon stop: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("daemon did not stop")
		}
	})
	waitFor(t, 3*time.Second, func() bool { _, err := os.Stat(configuration.ControlSocketPath); return err == nil })
	key, err := config.LoadSharedKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := ipc.NewAuthenticator(key)
	if err != nil {
		t.Fatal(err)
	}
	client := ipc.Client{SocketPath: configuration.ControlSocketPath, Authenticator: authenticator}
	var status reliability.RecoveryStatus
	if err := client.Call(ctx, ipc.OperationRecoveryStatus, struct{}{}, &status); err != nil {
		t.Fatal(err)
	}
	if status.Ready || !status.RecoveryRequired || status.LastRecovery == nil || status.LastRecovery.Succeeded {
		t.Fatalf("status=%#v", status)
	}
	var report reliability.RecoveryReport
	if err := client.Call(ctx, ipc.OperationRecoveryRun, struct{}{}, &report); err != nil {
		t.Fatal(err)
	}
	if report.Succeeded || report.FailedComponent != "journal_check" {
		t.Fatalf("report=%#v", report)
	}
	if err := client.Call(ctx, ipc.OperationNATApply, map[string]string{"transaction_id": "blocked_apply"}, nil); !ipc.IsRemoteError(err, "recovery_required") {
		t.Fatalf("apply error=%v", err)
	}
}
