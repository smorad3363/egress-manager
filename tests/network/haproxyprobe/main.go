package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	managed "github.com/egress-manager/egress-manager/internal/haproxy"
	"github.com/egress-manager/egress-manager/internal/system"
)

const (
	configPath  = "/tmp/egm-haproxy-managed.cfg"
	runtimePath = "/tmp/egm-haproxy-runtime.sock"
	pidPath     = "/tmp/egm-haproxy.pid"
)

func main() {
	backends := []domain.HAProxyBackend{
		{ID: "lab_primary", Name: "Lab Primary", Server: domain.Endpoint{Host: "10.203.2.2", Port: 18081}, Weight: 100, HealthCheck: true, Health: domain.Health{Status: domain.HealthUnknown}, Enabled: true},
		{ID: "lab_secondary", Name: "Lab Secondary", Server: domain.Endpoint{Host: "10.203.2.2", Port: 18082}, Weight: 100, HealthCheck: true, Health: domain.Health{Status: domain.HealthUnknown}, Enabled: true},
		{ID: "lab_backup", Name: "Lab Backup", Server: domain.Endpoint{Host: "10.203.2.2", Port: 18083}, Weight: 50, Backup: true, HealthCheck: true, Health: domain.Health{Status: domain.HealthUnknown}, Enabled: true},
	}
	frontends := []domain.HAProxyFrontend{{ID: "lab_proxy", Name: "Lab Proxy", Bind: netip.MustParseAddr("10.203.1.1"), Port: 18080, BackendIDs: []domain.ID{"lab_primary", "lab_secondary", "lab_backup"}, Algorithm: domain.BalanceRoundRobin, Enabled: true}}
	if os.Getenv("EGRESS_HAPROXY_STATS") == "1" {
		snapshot, err := runtimeClient().Snapshot(context.Background())
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := json.NewEncoder(os.Stdout).Encode(snapshot); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	content, readErr := os.ReadFile(configPath)
	exists := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		fmt.Fprintln(os.Stderr, readErr)
		os.Exit(1)
	}
	state, err := managed.ParseState(content, exists)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	plan, err := managed.BuildPlan(managed.Settings{RuntimeSocket: runtimePath, PIDFile: pidPath, MaxConnections: 4096, ConnectTimeout: time.Second, ClientTimeout: 5 * time.Second, ServerTimeout: 5 * time.Second, ProtectedPorts: []uint16{22, 443}}, frontends, backends, nil, state)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if os.Getenv("EGRESS_HAPROXY_EXECUTE") != "1" {
		fmt.Print(plan.Candidate)
		return
	}
	journal := &memoryJournal{}
	response, err := (managed.Executor{Runner: system.ExecRunner{}, Journal: journal, Runtime: runtimeClient(), ConfigPath: configPath, PIDPath: pidPath}).Execute(context.Background(), "haproxy_lab", `{}`, plan)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(response); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runtimeClient() managed.RuntimeClient {
	return managed.RuntimeClient{SocketPath: runtimePath, Dialer: managed.NetDialer{Dialer: net.Dialer{Timeout: time.Second}}, Timeout: 2 * time.Second}
}

type memoryJournal struct {
	mu        sync.Mutex
	operation domain.Transaction
	present   bool
}

func (journal *memoryJournal) CreateOperation(_ context.Context, operation domain.Transaction) error {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.present {
		return fmt.Errorf("operation already exists")
	}
	journal.operation, journal.present = operation, true
	return nil
}

func (journal *memoryJournal) TransitionOperation(_ context.Context, _ domain.ID, from, to domain.TransactionState, at time.Time, detail string) error {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if !journal.present || journal.operation.State != from {
		return fmt.Errorf("operation state mismatch")
	}
	journal.operation.State, journal.operation.UpdatedAt, journal.operation.FailureDetail = to, at, detail
	return nil
}

func (journal *memoryJournal) UnfinishedOperations(context.Context, int) ([]domain.Transaction, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if !journal.present {
		return []domain.Transaction{}, nil
	}
	return []domain.Transaction{journal.operation}, nil
}
