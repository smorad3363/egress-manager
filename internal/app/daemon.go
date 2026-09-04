package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/egress-manager/egress-manager/internal/buildinfo"
	"github.com/egress-manager/egress-manager/internal/config"
	"github.com/egress-manager/egress-manager/internal/database"
	"github.com/egress-manager/egress-manager/internal/domain"
	managedHAProxy "github.com/egress-manager/egress-manager/internal/haproxy"
	"github.com/egress-manager/egress-manager/internal/inventory"
	"github.com/egress-manager/egress-manager/internal/ipc"
	"github.com/egress-manager/egress-manager/internal/nat"
	"github.com/egress-manager/egress-manager/internal/secrets"
	managedSingBox "github.com/egress-manager/egress-manager/internal/singbox"
	"github.com/egress-manager/egress-manager/internal/system"
)

type DaemonOptions struct {
	ConfigPath string
	KeyPath    string
	Logger     *slog.Logger
}

func RunDaemon(ctx context.Context, options DaemonOptions) error {
	if ctx == nil {
		return fmt.Errorf("daemon context is required")
	}
	configuration, err := config.Load(options.ConfigPath)
	if err != nil {
		return err
	}
	key, err := config.LoadSharedKey(options.KeyPath)
	if err != nil {
		return err
	}
	authenticator, err := ipc.NewAuthenticator(key)
	if err != nil {
		return err
	}
	logger := options.Logger
	if logger == nil {
		logger = NewLogger(slog.LevelInfo)
	}
	databaseConnection, err := database.Open(ctx, configuration.DatabasePath)
	if err != nil {
		return err
	}
	defer databaseConnection.Close()
	protector, err := secrets.NewProtector(key, nil)
	if err != nil {
		return err
	}
	store, err := database.NewProtectedStore(databaseConnection, protector)
	if err != nil {
		return err
	}
	runner := system.ExecRunner{}
	executor := nat.Executor{Runner: runner, Journal: store, Verifier: nat.SystemVerifier{Runner: runner}}
	if err := executor.Recover(ctx); err != nil {
		return fmt.Errorf("recover interrupted network operations: %w", err)
	}
	haproxyRuntime := managedHAProxy.RuntimeClient{SocketPath: configuration.HAProxyRuntimeSocketPath, Dialer: managedHAProxy.NetDialer{}, Timeout: 2 * time.Second}
	haproxyExecutor := managedHAProxy.Executor{Runner: runner, Journal: store, Runtime: haproxyRuntime, ConfigPath: configuration.HAProxyConfigPath, PIDPath: configuration.HAProxyPIDPath}
	if err := haproxyExecutor.Recover(ctx); err != nil {
		return fmt.Errorf("recover interrupted HAProxy operations: %w", err)
	}
	singboxExecutor := managedSingBox.Executor{Runner: runner, Journal: store, Protector: protector, ConfigPath: configuration.SingBoxConfigPath}
	if err := singboxExecutor.Recover(ctx); err != nil {
		return fmt.Errorf("recover interrupted sing-box operations: %w", err)
	}
	collector := inventory.Collector{Runner: runner, Files: inventory.OSFiles{}, Timeout: 2 * time.Second}
	server, err := ipc.NewServer(authenticator, logger)
	if err != nil {
		return err
	}
	if err := server.Handle(ipc.OperationHealth, func(ctx context.Context, payload json.RawMessage) (any, error) {
		if err := decodeEmptyPayload(payload); err != nil {
			return nil, err
		}
		if err := databaseConnection.PingContext(ctx); err != nil {
			return nil, fmt.Errorf("database health check failed")
		}
		return map[string]string{"status": "ok", "version": buildinfo.Version}, nil
	}); err != nil {
		return err
	}
	if err := server.Handle(ipc.OperationInventory, func(ctx context.Context, payload json.RawMessage) (any, error) {
		if err := decodeEmptyPayload(payload); err != nil {
			return nil, err
		}
		return collector.Collect(ctx)
	}); err != nil {
		return err
	}

	var natMutex sync.Mutex
	buildPlan := func(ctx context.Context, request nat.PlanRequest) (nat.Plan, error) {
		hostInventory, err := collector.Collect(ctx)
		if err != nil {
			return nat.Plan{}, err
		}
		sshPorts := append([]uint16{}, configuration.SSHPorts...)
		for _, listener := range hostInventory.Listeners {
			if strings.EqualFold(listener.Process, "sshd") && listener.Protocol == "tcp" && listener.Port != 0 && !containsPort(sshPorts, listener.Port) {
				sshPorts = append(sshPorts, listener.Port)
			}
		}
		engine, tableExists, iptablesState, err := selectNATEngine(ctx, runner, hostInventory, request.Family)
		if err != nil {
			return nat.Plan{}, err
		}
		policy := nat.SafetyPolicy{
			SSHPorts:               sshPorts,
			PanelPort:              configuration.ListenPort,
			ProtectedLocalPrefixes: configuration.ProtectedManagementCIDRs,
		}
		if engine == "iptables" {
			return nat.BuildIPTablesPlan(request.Family, request.Forwards, hostInventory.Listeners, policy, iptablesState)
		}
		return nat.BuildNFTPlan(request.Family, request.Forwards, hostInventory.Listeners, policy, tableExists)
	}
	if err := server.Handle(ipc.OperationNATPlan, func(ctx context.Context, payload json.RawMessage) (any, error) {
		var request nat.PlanRequest
		if err := decodePayload(payload, &request); err != nil {
			return nil, err
		}
		natMutex.Lock()
		defer natMutex.Unlock()
		return buildPlan(ctx, request)
	}); err != nil {
		return err
	}
	if err := server.Handle(ipc.OperationNATApply, func(ctx context.Context, payload json.RawMessage) (any, error) {
		var request nat.ApplyRequest
		if err := decodePayload(payload, &request); err != nil {
			return nil, err
		}
		natMutex.Lock()
		defer natMutex.Unlock()
		plan, err := buildPlan(ctx, nat.PlanRequest{Family: request.Family, Forwards: request.Forwards})
		if err != nil {
			return nil, err
		}
		if err := executor.Execute(ctx, request.TransactionID, request.RequestedChange, plan); err != nil {
			return nil, err
		}
		return nat.ApplyResponse{TransactionID: request.TransactionID, State: domain.TransactionCommitted}, nil
	}); err != nil {
		return err
	}
	if err := server.Handle(ipc.OperationNATCount, func(ctx context.Context, payload json.RawMessage) (any, error) {
		var request nat.CounterRequest
		if err := decodePayload(payload, &request); err != nil {
			return nil, err
		}
		natMutex.Lock()
		defer natMutex.Unlock()
		hostInventory, err := collector.Collect(ctx)
		if err != nil {
			return nil, err
		}
		engine, _, _, err := selectNATEngine(ctx, runner, hostInventory, request.Family)
		if err != nil {
			return nil, err
		}
		if engine == "iptables" {
			return (nat.IPTablesCounterReader{Runner: runner}).Read(ctx, request.Family)
		}
		return (nat.CounterReader{Runner: runner}).Read(ctx, request.Family)
	}); err != nil {
		return err
	}

	var haproxyMutex sync.Mutex
	buildHAProxyPlan := func(ctx context.Context, request managedHAProxy.PlanRequest) (managedHAProxy.Plan, error) {
		hostInventory, err := collector.Collect(ctx)
		if err != nil {
			return managedHAProxy.Plan{}, err
		}
		state, err := managedHAProxy.InspectState(configuration.HAProxyConfigPath)
		if err != nil {
			return managedHAProxy.Plan{}, err
		}
		protectedPorts := append([]uint16{configuration.ListenPort}, configuration.SSHPorts...)
		for _, listener := range hostInventory.Listeners {
			if strings.EqualFold(listener.Process, "sshd") && listener.Protocol == "tcp" && listener.Port != 0 && !containsPort(protectedPorts, listener.Port) {
				protectedPorts = append(protectedPorts, listener.Port)
			}
		}
		return managedHAProxy.BuildPlan(managedHAProxy.Settings{RuntimeSocket: configuration.HAProxyRuntimeSocketPath, PIDFile: configuration.HAProxyPIDPath, MaxConnections: 100000, ConnectTimeout: 5 * time.Second, ClientTimeout: 30 * time.Second, ServerTimeout: 30 * time.Second, ProtectedPorts: protectedPorts}, request.Frontends, request.Backends, hostInventory.Listeners, state)
	}
	if err := server.Handle(ipc.OperationHAProxyPlan, func(ctx context.Context, payload json.RawMessage) (any, error) {
		var request managedHAProxy.PlanRequest
		if err := decodePayload(payload, &request); err != nil {
			return nil, err
		}
		haproxyMutex.Lock()
		defer haproxyMutex.Unlock()
		return buildHAProxyPlan(ctx, request)
	}); err != nil {
		return err
	}
	if err := server.Handle(ipc.OperationHAProxyApply, func(ctx context.Context, payload json.RawMessage) (any, error) {
		var request managedHAProxy.ApplyRequest
		if err := decodePayload(payload, &request); err != nil {
			return nil, err
		}
		haproxyMutex.Lock()
		defer haproxyMutex.Unlock()
		plan, err := buildHAProxyPlan(ctx, managedHAProxy.PlanRequest{Frontends: request.Frontends, Backends: request.Backends})
		if err != nil {
			return nil, err
		}
		return haproxyExecutor.Execute(ctx, request.TransactionID, request.RequestedChange, plan)
	}); err != nil {
		return err
	}
	if err := server.Handle(ipc.OperationHAProxyStats, func(ctx context.Context, payload json.RawMessage) (any, error) {
		if err := decodeEmptyPayload(payload); err != nil {
			return nil, err
		}
		haproxyMutex.Lock()
		defer haproxyMutex.Unlock()
		return haproxyRuntime.Snapshot(ctx)
	}); err != nil {
		return err
	}

	listener, err := ipc.ListenUnix(configuration.ControlSocketPath)
	if err != nil {
		return err
	}
	defer listener.Close()
	identity, err := os.Lstat(configuration.ControlSocketPath)
	if err != nil {
		return fmt.Errorf("inspect created IPC socket: %w", err)
	}
	defer removeOwnedSocket(configuration.ControlSocketPath, identity, logger)
	logger.Info("egressd ready", "socket_path", configuration.ControlSocketPath, "version", buildinfo.Version)
	return server.Serve(ctx, listener)
}

func selectNATEngine(ctx context.Context, runner system.Runner, hostInventory inventory.Inventory, family nat.AddressFamily) (string, bool, nat.IPTablesState, error) {
	nftAvailable := capabilityAvailable(hostInventory, "nftables")
	iptablesAvailable := capabilityAvailable(hostInventory, "iptables")
	var nftExists bool
	var iptablesState = nat.IPTablesState{NatRules: []string{}, FilterRules: []string{}}
	var err error
	if nftAvailable {
		nftExists, err = nat.InspectOwnedTable(ctx, runner, family, 2*time.Second)
		if err != nil {
			return "", false, nat.IPTablesState{}, err
		}
	}
	if iptablesAvailable {
		inspectedState, inspectErr := nat.InspectIPTablesState(ctx, runner, family, 2*time.Second)
		if inspectErr != nil && !nftAvailable {
			return "", false, nat.IPTablesState{}, inspectErr
		}
		if inspectErr == nil {
			iptablesState = inspectedState
		}
	}
	iptableExists := iptablesState.PreroutingChain || iptablesState.PostroutingChain || iptablesState.ForwardChain
	if nftExists && iptableExists {
		return "", false, nat.IPTablesState{}, fmt.Errorf("both nftables and iptables Egress Manager state exist; refusing ambiguous mutation")
	}
	if iptableExists || !nftAvailable && iptablesAvailable {
		return "iptables", false, iptablesState, nil
	}
	if nftAvailable {
		return "nftables", nftExists, iptablesState, nil
	}
	return "", false, nat.IPTablesState{}, fmt.Errorf("no compatible nftables or iptables engine is available")
}

func capabilityAvailable(hostInventory inventory.Inventory, name string) bool {
	for _, capability := range hostInventory.Capabilities {
		if strings.EqualFold(capability.Name, name) {
			return capability.Available
		}
	}
	return false
}

func decodeEmptyPayload(payload json.RawMessage) error {
	var input struct{}
	return decodePayload(payload, &input)
}

func decodePayload(payload json.RawMessage, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("decode IPC payload: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("decode IPC payload: trailing data")
	}
	return nil
}

func containsPort(ports []uint16, candidate uint16) bool {
	for _, port := range ports {
		if port == candidate {
			return true
		}
	}
	return false
}

func removeOwnedSocket(path string, identity os.FileInfo, logger *slog.Logger) {
	current, err := os.Lstat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Warn("inspect IPC socket during cleanup failed", "error_type", fmt.Sprintf("%T", err))
		}
		return
	}
	if current.Mode()&os.ModeSocket == 0 || !os.SameFile(identity, current) {
		logger.Warn("IPC socket path changed; refusing cleanup")
		return
	}
	if err := os.Remove(path); err != nil {
		logger.Warn("remove IPC socket failed", "error_type", fmt.Sprintf("%T", err))
	}
}
