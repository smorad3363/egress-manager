package interfaceoutbound

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/database"
	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/inventory"
	"github.com/egress-manager/egress-manager/internal/secrets"
	"github.com/egress-manager/egress-manager/internal/system"
)

type lifecycleRunner struct {
	links          map[string]string
	services       map[string]string
	failValidation bool
	failOpenVPN    bool
}

func newLifecycleRunner() *lifecycleRunner {
	return &lifecycleRunner{links: map[string]string{}, services: map[string]string{}}
}

func (runner *lifecycleRunner) Run(_ context.Context, command system.Command) (system.Result, error) {
	args := command.Args
	switch command.Name {
	case "wg-quick", "openvpn":
		if runner.failValidation && (command.Name == "wg-quick" || containsArg(args, "--show-tls")) {
			return system.Result{ExitCode: 1}, errors.New("validation failed")
		}
		return system.Result{ExitCode: 0}, nil
	case "ip":
		if len(args) == 6 && args[0] == "-json" && args[3] == "show" {
			name := args[5]
			kind, exists := runner.links[name]
			if !exists {
				return system.Result{ExitCode: 1}, errors.New("not found")
			}
			output, _ := json.Marshal([]map[string]any{{"ifname": name, "linkinfo": map[string]string{"info_kind": kind}}})
			return system.Result{ExitCode: 0, Stdout: output}, nil
		}
		if len(args) >= 6 && args[0] == "link" && args[1] == "add" {
			runner.links[args[3]] = args[5]
			return system.Result{ExitCode: 0}, nil
		}
		if len(args) == 4 && args[0] == "link" && args[1] == "delete" {
			delete(runner.links, args[3])
			return system.Result{ExitCode: 0}, nil
		}
		return system.Result{ExitCode: 0}, nil
	case "wg":
		return system.Result{ExitCode: 0}, nil
	case "systemd-run":
		if runner.failOpenVPN {
			return system.Result{ExitCode: 1}, errors.New("service failed")
		}
		unit := argAfter(args, "--unit")
		configPath := argAfter(args, "--config")
		content, err := os.ReadFile(configPath)
		if err != nil {
			return system.Result{ExitCode: 1}, err
		}
		device := configDevice(string(content))
		if unit == "" || device == "" {
			return system.Result{ExitCode: 1}, errors.New("invalid service")
		}
		runner.services[unit] = device
		runner.links[device] = "tun"
		return system.Result{ExitCode: 0}, nil
	case "systemctl":
		if len(args) == 2 && args[0] == "stop" {
			device, exists := runner.services[args[1]]
			if !exists {
				return system.Result{ExitCode: 5}, errors.New("not loaded")
			}
			delete(runner.services, args[1])
			delete(runner.links, device)
			return system.Result{ExitCode: 0}, nil
		}
		if len(args) == 3 && args[0] == "is-active" {
			if _, exists := runner.services[args[2]]; exists {
				return system.Result{ExitCode: 0}, nil
			}
			return system.Result{ExitCode: 3}, errors.New("inactive")
		}
	}
	return system.Result{ExitCode: 1}, fmt.Errorf("unexpected command %s %s", command.Name, strings.Join(args, " "))
}

func TestExecutorAppliesAndVerifiesNativeInterfaces(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	runner := newLifecycleRunner()
	executor := testLifecycleExecutor(t, root, runner)
	plan, _, _ := lifecyclePlan(t, executor, nil, true)
	response, err := executor.Execute(context.Background(), "interface_apply", plan)
	if err != nil {
		t.Fatal(err)
	}
	if response.State != domain.TransactionCommitted || response.CandidateHash != plan.Review.CandidateHash {
		t.Fatalf("response = %#v", response)
	}
	state, err := InspectState(executor.StatePath, executor.RuntimeDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if state.Hash != plan.Review.CandidateHash || len(state.Entries) != 2 {
		t.Fatalf("state = %#v", state)
	}
	for _, entry := range state.Entries {
		if _, exists := runner.links[entry.InterfaceName]; !exists {
			t.Fatalf("interface %q was not created", entry.InterfaceName)
		}
	}
}

func TestExecutorRecreatesMissingRuntimeWithUnchangedDesiredState(t *testing.T) {
	t.Parallel()
	runner := newLifecycleRunner()
	executor := testLifecycleExecutor(t, t.TempDir(), runner)
	initial, _, _ := lifecyclePlan(t, executor, nil, true)
	ctx := context.Background()
	if _, err := executor.Execute(ctx, "interface_before_restart", initial); err != nil {
		t.Fatal(err)
	}
	// Reboot loses kernel links and transient services, but preserves private files.
	clear(runner.links)
	clear(runner.services)
	plan, _, _ := lifecyclePlan(t, executor, nil, true)
	if plan.Review.StateHash != plan.Review.CandidateHash {
		t.Fatal("unchanged desired state changed its hash")
	}
	if err := executor.VerifyRuntime(ctx, plan); err == nil {
		t.Fatal("missing runtime accepted because files survived")
	}
	if _, err := executor.Execute(ctx, "interface_after_restart", plan); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := executor.VerifyRuntime(ctx, plan); err != nil {
			t.Fatal(err)
		}
	}
	if len(runner.links) != 2 || len(runner.services) != 1 {
		t.Fatalf("runtime did not converge: links=%d services=%d", len(runner.links), len(runner.services))
	}
	for name, kind := range runner.links {
		if kind == "wireguard" {
			runner.links[name] = "dummy"
		}
	}
	if err := executor.VerifyRuntime(ctx, plan); err == nil {
		t.Fatal("foreign link kind accepted as a healthy owned WireGuard interface")
	}
}

func TestExecutorRollsBackPartialNativeApply(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	runner := newLifecycleRunner()
	executor := testLifecycleExecutor(t, root, runner)
	initial, _, wireGuard := lifecyclePlan(t, executor, nil, false)
	if _, err := executor.Execute(context.Background(), "interface_initial", initial); err != nil {
		t.Fatal(err)
	}
	previous, err := InspectState(executor.StatePath, executor.RuntimeDirectory)
	if err != nil {
		t.Fatal(err)
	}
	next, _, _ := lifecyclePlan(t, executor, []inventory.Interface{{Name: previous.Entries[0].InterfaceName, State: "up"}}, true)
	runner.failOpenVPN = true
	if _, err := executor.Execute(context.Background(), "interface_rollback", next); err == nil {
		t.Fatal("failed OpenVPN start did not fail apply")
	}
	restored, err := InspectState(executor.StatePath, executor.RuntimeDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Hash != previous.Hash {
		t.Fatalf("state was not restored: got %s want %s", restored.Hash, previous.Hash)
	}
	wireGuardCredential, _ := DecodeCredential(wireGuard.CredentialDocument)
	if runner.links[wireGuardCredential.InterfaceName] != "wireguard" {
		t.Fatal("previous WireGuard interface was not restored")
	}
}

func TestExecutorRejectsStaleStateAndValidationFailureWithoutMutation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	runner := newLifecycleRunner()
	executor := testLifecycleExecutor(t, root, runner)
	stale, _, _ := lifecyclePlan(t, executor, nil, false)
	if _, err := executor.Execute(context.Background(), "interface_current", stale); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(context.Background(), "interface_stale", stale); !errors.Is(err, ErrStateChanged) {
		t.Fatalf("stale plan error = %v", err)
	}
	state, err := InspectState(executor.StatePath, executor.RuntimeDirectory)
	if err != nil {
		t.Fatal(err)
	}
	outbounds, credentials := importedOutbounds(t, true)
	interfaces := []inventory.Interface{{Name: state.Entries[0].InterfaceName, State: "up"}}
	plan, err := BuildPlan(outbounds, credentials, state, interfaces)
	if err != nil {
		t.Fatal(err)
	}
	runner.failValidation = true
	if _, err := executor.Execute(context.Background(), "interface_invalid", plan); err == nil {
		t.Fatal("native validation failure accepted")
	}
	unchanged, err := InspectState(executor.StatePath, executor.RuntimeDirectory)
	if err != nil || unchanged.Hash != state.Hash {
		t.Fatalf("validation failure mutated state: %#v %v", unchanged, err)
	}
}

func TestExecutorRecoversInterruptedNativeApply(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	runner := newLifecycleRunner()
	executor := testLifecycleExecutor(t, root, runner)
	initial, _, _ := lifecyclePlan(t, executor, nil, false)
	if _, err := executor.Execute(context.Background(), "interface_recovery_base", initial); err != nil {
		t.Fatal(err)
	}
	previousState, err := InspectState(executor.StatePath, executor.RuntimeDirectory)
	if err != nil {
		t.Fatal(err)
	}
	next, _, _ := lifecyclePlan(t, executor, []inventory.Interface{{Name: previousState.Entries[0].InterfaceName, State: "up"}}, true)
	previous, err := executor.snapshot(previousState)
	if err != nil {
		t.Fatal(err)
	}
	candidate := snapshotFromPlan(next)
	operationID := domain.ID("interface_recovery")
	protectedPrevious, err := executor.protect(operationID, "snapshot", previous)
	if err != nil {
		t.Fatal(err)
	}
	protectedCandidate, err := executor.protect(operationID, "candidate", candidate)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1700000000, 0).UTC()
	operation := domain.Transaction{ID: operationID, Operation: "interface_outbound_apply", State: domain.TransactionPrepared, RequestedChange: "test interrupted native apply", PreviousSnapshot: protectedPrevious, CandidateConfig: protectedCandidate, CreatedAt: now, UpdatedAt: now}
	if err := executor.Journal.CreateOperation(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if err := executor.Journal.TransitionOperation(context.Background(), operationID, domain.TransactionPrepared, domain.TransactionValidated, now, ""); err != nil {
		t.Fatal(err)
	}
	if err := executor.Journal.TransitionOperation(context.Background(), operationID, domain.TransactionValidated, domain.TransactionApplying, now, ""); err != nil {
		t.Fatal(err)
	}
	if err := executor.apply(context.Background(), previousState, next); err != nil {
		t.Fatal(err)
	}
	if err := executor.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored, err := InspectState(executor.StatePath, executor.RuntimeDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Hash != previousState.Hash || len(restored.Entries) != 1 || restored.Entries[0].Kind != domain.OutboundWireGuard {
		t.Fatalf("recovery did not restore prior native state: %#v", restored)
	}
}

func testLifecycleExecutor(t *testing.T, root string, runner system.Runner) Executor {
	t.Helper()
	databaseConnection, err := database.Open(context.Background(), filepath.Join(root, "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = databaseConnection.Close() })
	protector, err := secrets.NewProtector([32]byte{9, 8, 7, 6}, bytes.NewReader(bytes.Repeat([]byte{4}, 8192)))
	if err != nil {
		t.Fatal(err)
	}
	runtimeDirectory := filepath.Join(root, "runtime")
	return Executor{Runner: runner, Journal: database.NewStore(databaseConnection), Protector: protector, StatePath: filepath.Join(runtimeDirectory, "state.json"), RuntimeDirectory: runtimeDirectory}
}

func lifecyclePlan(t *testing.T, executor Executor, interfaces []inventory.Interface, includeOpenVPN bool) (ExecutionPlan, []domain.Outbound, ImportResult) {
	t.Helper()
	outbounds, credentials := importedOutbounds(t, includeOpenVPN)
	state, err := InspectState(executor.StatePath, executor.RuntimeDirectory)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(outbounds, credentials, state, interfaces)
	if err != nil {
		t.Fatal(err)
	}
	wireGuard, err := ParseImport(readFixture(t, "wireguard.conf"))
	if err != nil {
		t.Fatal(err)
	}
	return plan, outbounds, wireGuard
}

func importedOutbounds(t *testing.T, includeOpenVPN bool) ([]domain.Outbound, map[domain.ID][]byte) {
	t.Helper()
	wireGuard, err := ParseImport(readFixture(t, "wireguard.conf"))
	if err != nil {
		t.Fatal(err)
	}
	results := []ImportResult{wireGuard}
	if includeOpenVPN {
		openVPN, err := ParseImport(readFixture(t, "client.ovpn"))
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, openVPN)
	}
	outbounds := make([]domain.Outbound, 0, len(results))
	credentials := make(map[domain.ID][]byte, len(results))
	for _, result := range results {
		outbounds = append(outbounds, result.Outbound)
		credentials[result.Outbound.ID] = result.CredentialDocument
	}
	return outbounds, credentials
}

func containsArg(args []string, expected string) bool {
	for _, value := range args {
		if value == expected {
			return true
		}
	}
	return false
}

func argAfter(args []string, expected string) string {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == expected {
			return args[index+1]
		}
	}
	return ""
}

func configDevice(config string) string {
	for _, line := range strings.Split(config, "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "dev" {
			return fields[1]
		}
	}
	return ""
}
