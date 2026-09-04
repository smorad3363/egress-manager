package routeengine

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/database"
	"github.com/egress-manager/egress-manager/internal/domain"
	managedInterface "github.com/egress-manager/egress-manager/internal/interfaceoutbound"
	"github.com/egress-manager/egress-manager/internal/routing"
	"github.com/egress-manager/egress-manager/internal/secrets"
	"github.com/egress-manager/egress-manager/internal/system"
)

type routeEngineRunner struct {
	t              *testing.T
	nftInstalled   bool
	ipv4Installed  bool
	ipv6Installed  bool
	failValidation bool
	failIPv6       bool
}

func (runner *routeEngineRunner) Run(_ context.Context, command system.Command) (system.Result, error) {
	runner.t.Helper()
	joined := command.Name + " " + strings.Join(command.Args, " ")
	switch {
	case joined == "nft list tables":
		if runner.nftInstalled {
			return system.Result{Stdout: []byte("table inet egm_egress\n"), ExitCode: 0}, nil
		}
		return system.Result{ExitCode: 0}, nil
	case joined == "nft list table inet egm_egress":
		return system.Result{Stdout: []byte("table inet egm_egress {\n}\n"), ExitCode: 0}, nil
	case joined == "nft --check --file -":
		if runner.failValidation {
			return system.Result{ExitCode: 1}, errors.New("invalid nft candidate")
		}
		return system.Result{ExitCode: 0}, nil
	case joined == "nft --file -":
		runner.nftInstalled = strings.Contains(string(command.Stdin), "table inet egm_egress {")
		return system.Result{ExitCode: 0}, nil
	case strings.HasPrefix(joined, "sing-box check -c "):
		return system.Result{ExitCode: 0}, nil
	case joined == "systemctl restart egress-manager-sing-box.service", joined == "systemctl is-active --quiet egress-manager-sing-box.service":
		return system.Result{ExitCode: 0}, nil
	case joined == "systemctl stop egress-manager-sing-box.service":
		return system.Result{ExitCode: 0}, nil
	case strings.HasPrefix(joined, "ip -j link show dev "):
		name := command.Args[len(command.Args)-1]
		return system.Result{Stdout: []byte(`[{"ifname":"` + name + `"}]`), ExitCode: 0}, nil
	case joined == "ip -4 -batch -":
		runner.ipv4Installed = true
		return system.Result{ExitCode: 0}, nil
	case joined == "ip -6 -batch -":
		if runner.failIPv6 {
			runner.failIPv6 = false
			return system.Result{ExitCode: 2}, errors.New("interrupted IPv6 apply")
		}
		runner.ipv6Installed = true
		return system.Result{ExitCode: 0}, nil
	case strings.HasPrefix(joined, "ip -4 rule delete priority "):
		runner.ipv4Installed = false
		return system.Result{ExitCode: 0}, nil
	case strings.HasPrefix(joined, "ip -6 rule delete priority "):
		runner.ipv6Installed = false
		return system.Result{ExitCode: 0}, nil
	case strings.HasPrefix(joined, "ip -4 route flush table "), strings.HasPrefix(joined, "ip -6 route flush table "):
		return system.Result{ExitCode: 0}, nil
	case strings.HasPrefix(joined, "ip -j -4 rule show priority "), strings.HasPrefix(joined, "ip -j -4 route show table "):
		if runner.ipv4Installed {
			return ownedProtocolResult(), nil
		}
		return system.Result{Stdout: []byte("[]"), ExitCode: 0}, nil
	case strings.HasPrefix(joined, "ip -j -6 rule show priority "), strings.HasPrefix(joined, "ip -j -6 route show table "):
		if runner.ipv6Installed {
			return ownedProtocolResult(), nil
		}
		return system.Result{Stdout: []byte("[]"), ExitCode: 0}, nil
	default:
		runner.t.Fatalf("unexpected command: %s", joined)
		return system.Result{ExitCode: -1}, nil
	}
}

func ownedProtocolResult() system.Result {
	return system.Result{Stdout: []byte(`[{"protocol":"` + routing.OwnedRouteProtocol + `"}]`), ExitCode: 0}
}

func routeEngineStore(t *testing.T) (*database.Store, *sql.DB) {
	t.Helper()
	connection, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "route-engine.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return database.NewStore(connection), connection
}

func routeEngineProtector(t *testing.T) *secrets.Protector {
	t.Helper()
	protector, err := secrets.NewProtector([32]byte{8, 7, 6, 5}, strings.NewReader(strings.Repeat("r", 4096)))
	if err != nil {
		t.Fatal(err)
	}
	return protector
}

func TestExecutorCommitsCoordinatedCandidates(t *testing.T) {
	t.Parallel()
	plan := coordinatedTestPlan(t)
	store, _ := routeEngineStore(t)
	directory := t.TempDir()
	runner := &routeEngineRunner{t: t}
	executor := Executor{
		Runner: runner, Journal: store, Protector: routeEngineProtector(t),
		SingBoxConfigPath: filepath.Join(directory, "sing-box.json"), RoutingStatePath: filepath.Join(directory, "routing.json"),
		InterfaceStatePath: filepath.Join(directory, "interfaces", "state.json"), InterfaceRuntimeDirectory: filepath.Join(directory, "interfaces"),
		Now: func() time.Time { return time.Unix(1_800_000_000, 0) },
	}
	response, err := executor.Execute(context.Background(), "route_success", plan)
	if err != nil {
		t.Fatal(err)
	}
	if response.State != domain.TransactionCommitted || response.CombinedCandidateHash != plan.Review.CombinedCandidateHash || !runner.nftInstalled || !runner.ipv4Installed || !runner.ipv6Installed {
		t.Fatalf("response = %#v", response)
	}
	operation, err := store.Operation(context.Background(), "route_success")
	if err != nil {
		t.Fatal(err)
	}
	if operation.State != domain.TransactionCommitted || strings.Contains(operation.PreviousSnapshot+operation.CandidateConfig+operation.RequestedChange, "user:pass") {
		t.Fatalf("operation = %#v", operation)
	}
	stateContent, err := os.ReadFile(executor.RoutingStatePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := routing.ParseState(stateContent, true); err != nil {
		t.Fatal(err)
	}
}

func TestExecutorRejectsNativeValidationBeforeMutation(t *testing.T) {
	t.Parallel()
	plan := coordinatedTestPlan(t)
	store, _ := routeEngineStore(t)
	directory := t.TempDir()
	runner := &routeEngineRunner{t: t, failValidation: true}
	executor := Executor{Runner: runner, Journal: store, Protector: routeEngineProtector(t), SingBoxConfigPath: filepath.Join(directory, "sing-box.json"), RoutingStatePath: filepath.Join(directory, "routing.json"), InterfaceStatePath: filepath.Join(directory, "interfaces", "state.json"), InterfaceRuntimeDirectory: filepath.Join(directory, "interfaces")}
	if _, err := executor.Execute(context.Background(), "route_invalid", plan); err == nil {
		t.Fatal("Execute() accepted failed native validation")
	}
	operation, err := store.Operation(context.Background(), "route_invalid")
	if err != nil {
		t.Fatal(err)
	}
	if operation.State != domain.TransactionFailed || operation.FailureDetail != "native_validation_failed" || runner.nftInstalled || runner.ipv4Installed || runner.ipv6Installed {
		t.Fatalf("operation = %#v", operation)
	}
}

func TestExecutorRejectsChangedInterfaceStateBeforeMutation(t *testing.T) {
	t.Parallel()
	plan := coordinatedTestPlan(t)
	store, _ := routeEngineStore(t)
	directory := t.TempDir()
	runtimeDirectory := filepath.Join(directory, "interfaces")
	if err := os.MkdirAll(runtimeDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	interfaceName, err := managedInterface.InterfaceName(domain.OutboundWireGuard, "changed_wg")
	if err != nil {
		t.Fatal(err)
	}
	configuration := []byte("changed native configuration")
	digest := sha256.Sum256(configuration)
	state := []byte(`{"schema":"egress-manager/interface-outbounds/v1","entries":[{"id":"changed_wg","kind":"wireguard","interface_name":"` + interfaceName + `","config_hash":"` + hex.EncodeToString(digest[:]) + `","addresses":["10.0.0.2/32"]}]}`)
	if err := os.WriteFile(filepath.Join(runtimeDirectory, "changed_wg.wg.conf"), configuration, 0o600); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(runtimeDirectory, "state.json")
	if err := os.WriteFile(statePath, state, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &routeEngineRunner{t: t}
	executor := Executor{Runner: runner, Journal: store, Protector: routeEngineProtector(t), SingBoxConfigPath: filepath.Join(directory, "sing-box.json"), RoutingStatePath: filepath.Join(directory, "routing.json"), InterfaceStatePath: statePath, InterfaceRuntimeDirectory: runtimeDirectory}
	if _, err := executor.Execute(context.Background(), "route_stale_interface", plan); !errors.Is(err, ErrStateChanged) {
		t.Fatalf("Execute() error = %v, want ErrStateChanged", err)
	}
}

func TestExecutorRollsBackPartialIPApply(t *testing.T) {
	t.Parallel()
	plan := coordinatedTestPlan(t)
	store, _ := routeEngineStore(t)
	directory := t.TempDir()
	runner := &routeEngineRunner{t: t, failIPv6: true}
	executor := Executor{Runner: runner, Journal: store, Protector: routeEngineProtector(t), SingBoxConfigPath: filepath.Join(directory, "sing-box.json"), RoutingStatePath: filepath.Join(directory, "routing.json"), InterfaceStatePath: filepath.Join(directory, "interfaces", "state.json"), InterfaceRuntimeDirectory: filepath.Join(directory, "interfaces")}
	if _, err := executor.Execute(context.Background(), "route_rollback", plan); err == nil || !strings.Contains(err.Error(), "interrupted IPv6 apply") {
		t.Fatalf("Execute() error = %v", err)
	}
	operation, err := store.Operation(context.Background(), "route_rollback")
	if err != nil {
		t.Fatal(err)
	}
	if operation.State != domain.TransactionRolledBack || operation.FailureDetail != "iproute_apply_failed" || runner.nftInstalled || runner.ipv4Installed || runner.ipv6Installed {
		t.Fatalf("operation = %#v", operation)
	}
	if _, err := os.Stat(executor.SingBoxConfigPath); !os.IsNotExist(err) {
		t.Fatalf("rolled-back sing-box file error = %v", err)
	}
}

func TestExecutorRecoversInterruptedApply(t *testing.T) {
	t.Parallel()
	plan := coordinatedTestPlan(t)
	store, _ := routeEngineStore(t)
	protector := routeEngineProtector(t)
	directory := t.TempDir()
	runner := &routeEngineRunner{t: t, nftInstalled: true, ipv4Installed: true, ipv6Installed: true}
	executor := Executor{Runner: runner, Journal: store, Protector: protector, SingBoxConfigPath: filepath.Join(directory, "sing-box.json"), RoutingStatePath: filepath.Join(directory, "routing.json"), InterfaceStatePath: filepath.Join(directory, "interfaces", "state.json"), InterfaceRuntimeDirectory: filepath.Join(directory, "interfaces")}
	if err := os.WriteFile(executor.SingBoxConfigPath, plan.singBox.Candidate(), 0o600); err != nil {
		t.Fatal(err)
	}
	protected, err := executor.protectSnapshot("route_recover", fileSnapshot{}, fileSnapshot{}, fileSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	protectedJSON, _ := json.Marshal(protected)
	candidateEnvelope, err := protector.Seal(journalContext("route_recover", "candidate-routing"), plan.routing.Candidate())
	if err != nil {
		t.Fatal(err)
	}
	candidateJSON, _ := json.Marshal(candidateEnvelope)
	now := time.Now().UTC().Truncate(time.Second)
	operation := domain.Transaction{ID: "route_recover", Operation: "route_engine_apply", State: domain.TransactionPrepared, RequestedChange: "recover route", PreviousSnapshot: string(protectedJSON), CandidateConfig: string(candidateJSON), CreatedAt: now, UpdatedAt: now}
	if err := store.CreateOperation(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if err := store.TransitionOperation(context.Background(), operation.ID, domain.TransactionPrepared, domain.TransactionValidated, now, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.TransitionOperation(context.Background(), operation.ID, domain.TransactionValidated, domain.TransactionApplying, now, ""); err != nil {
		t.Fatal(err)
	}
	if err := executor.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Operation(context.Background(), operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != domain.TransactionRolledBack || loaded.FailureDetail != "interrupted_operation" || runner.nftInstalled || runner.ipv4Installed || runner.ipv6Installed {
		t.Fatalf("recovered operation = %#v", loaded)
	}
}
