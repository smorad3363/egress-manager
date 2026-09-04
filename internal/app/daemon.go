package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/egress-manager/egress-manager/internal/buildinfo"
	"github.com/egress-manager/egress-manager/internal/config"
	"github.com/egress-manager/egress-manager/internal/database"
	"github.com/egress-manager/egress-manager/internal/domain"
	managedHAProxy "github.com/egress-manager/egress-manager/internal/haproxy"
	managedInterface "github.com/egress-manager/egress-manager/internal/interfaceoutbound"
	"github.com/egress-manager/egress-manager/internal/inventory"
	"github.com/egress-manager/egress-manager/internal/ipc"
	"github.com/egress-manager/egress-manager/internal/logging"
	"github.com/egress-manager/egress-manager/internal/nat"
	"github.com/egress-manager/egress-manager/internal/reliability"
	"github.com/egress-manager/egress-manager/internal/routeengine"
	"github.com/egress-manager/egress-manager/internal/routing"
	"github.com/egress-manager/egress-manager/internal/secrets"
	managedSingBox "github.com/egress-manager/egress-manager/internal/singbox"
	"github.com/egress-manager/egress-manager/internal/system"
	managedXray "github.com/egress-manager/egress-manager/internal/xray"
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
	mutationLock := reliability.FileLock{Path: configuration.OperationLockPath}
	startupLease, err := mutationLock.TryAcquire("startup_recovery", "startup")
	if err != nil {
		return fmt.Errorf("acquire startup recovery lock: %w", err)
	}
	defer func() {
		if releaseErr := startupLease.Release(); releaseErr != nil {
			logger.Error("release startup recovery lock", "error", releaseErr)
		}
	}()
	runner := system.ExecRunner{}
	xrayDiscoverer := managedXray.Discoverer{Runner: runner, Files: managedXray.OSFileSystem{}, Timeout: 2 * time.Second}
	executor := nat.Executor{Runner: runner, Journal: store, Verifier: nat.SystemVerifier{Runner: runner}}
	haproxyRuntime := managedHAProxy.RuntimeClient{SocketPath: configuration.HAProxyRuntimeSocketPath, Dialer: managedHAProxy.NetDialer{}, Timeout: 2 * time.Second}
	haproxyExecutor := managedHAProxy.Executor{Runner: runner, Journal: store, Runtime: haproxyRuntime, ConfigPath: configuration.HAProxyConfigPath, PIDPath: configuration.HAProxyPIDPath}
	singboxExecutor := managedSingBox.Executor{Runner: runner, Journal: store, Protector: protector, ConfigPath: configuration.SingBoxConfigPath}
	interfaceExecutor := managedInterface.Executor{Runner: runner, Journal: store, Protector: protector, StatePath: configuration.InterfaceStatePath, RuntimeDirectory: configuration.InterfaceRuntimeDirectory}
	routeExecutor := routeengine.Executor{Runner: runner, Journal: store, Protector: protector, SingBoxConfigPath: configuration.SingBoxConfigPath, RoutingStatePath: configuration.RoutingStatePath, InterfaceStatePath: configuration.InterfaceStatePath, InterfaceRuntimeDirectory: configuration.InterfaceRuntimeDirectory}
	collector := inventory.Collector{Runner: runner, Files: inventory.OSFiles{}, Timeout: 2 * time.Second}
	loadInterfacePlan := func(ctx context.Context) (managedInterface.ExecutionPlan, error) {
		stored, err := store.ListOutbounds(ctx, "", managedInterface.MaximumOutbounds+1)
		if err != nil {
			return managedInterface.ExecutionPlan{}, err
		}
		if len(stored) > managedInterface.MaximumOutbounds {
			return managedInterface.ExecutionPlan{}, fmt.Errorf("interface outbound desired state exceeds %d outbounds", managedInterface.MaximumOutbounds)
		}
		outbounds := make([]domain.Outbound, 0, len(stored))
		credentials := make(map[domain.ID][]byte)
		for _, item := range stored {
			outbounds = append(outbounds, item.Outbound)
			if item.Outbound.Enabled && item.Outbound.Adapter == domain.OutboundAdapterInterface {
				document, credentialErr := store.OutboundCredential(ctx, item.Outbound.ID)
				if credentialErr != nil {
					return managedInterface.ExecutionPlan{}, credentialErr
				}
				credentials[item.Outbound.ID] = document
			}
		}
		state, err := managedInterface.InspectState(configuration.InterfaceStatePath, configuration.InterfaceRuntimeDirectory)
		if err != nil {
			return managedInterface.ExecutionPlan{}, err
		}
		var interfaces []inventory.Interface
		if len(credentials) > 0 || len(state.Entries) > 0 {
			host, collectErr := collector.Collect(ctx)
			if collectErr != nil {
				return managedInterface.ExecutionPlan{}, collectErr
			}
			interfaces = host.Interfaces
		}
		return managedInterface.BuildPlan(outbounds, credentials, state, interfaces)
	}
	reconcileInterfaces := func(ctx context.Context) error {
		plan, err := loadInterfacePlan(ctx)
		if err != nil {
			return fmt.Errorf("plan interface outbound reconciliation: %w", err)
		}
		if plan.Review.StateHash == plan.Review.CandidateHash && interfaceExecutor.VerifyRuntime(ctx, plan) == nil {
			return nil
		}
		operationID, err := logging.NewOperationID(nil)
		if err != nil {
			return err
		}
		_, err = interfaceExecutor.Execute(ctx, domain.ID("interface_reconcile_"+operationID), plan)
		return err
	}
	recoverXray := func(ctx context.Context) error {
		unfinished, err := store.UnfinishedOperations(ctx, reliability.MaximumRecoveryOperations)
		if err != nil {
			return err
		}
		for _, operation := range unfinished {
			if operation.Operation != "xray_fragment_apply" {
				continue
			}
			report, err := xrayDiscoverer.Discover(ctx)
			if err != nil {
				return fmt.Errorf("discover Xray for recovery: %w", err)
			}
			installation, err := selectManagedXrayInstallation(report)
			if err != nil {
				return err
			}
			xrayExecutor := managedXray.FragmentExecutor{Runner: runner, Journal: store, Protector: protector, Installation: installation}
			return xrayExecutor.Recover(ctx)
		}
		return nil
	}
	verifyRecoveryJournal := func(ctx context.Context) error {
		unfinished, err := store.UnfinishedOperations(ctx, reliability.MaximumRecoveryOperations)
		if err != nil {
			return err
		}
		if len(unfinished) != 0 {
			return fmt.Errorf("%d unfinished operations remain after recovery", len(unfinished))
		}
		return nil
	}
	recoveryCoordinator := reliability.Coordinator{Steps: []reliability.RecoveryStep{
		{Component: "interface", Recover: interfaceExecutor.Recover},
		{Component: "singbox", Recover: singboxExecutor.Recover},
		{Component: "haproxy", Recover: haproxyExecutor.Recover},
		{Component: "xray", Recover: recoverXray},
		{Component: "routing", Recover: routeExecutor.Recover},
		{Component: "nat", Recover: executor.Recover},
		{Component: "journal_check", Recover: verifyRecoveryJournal},
		{Component: "interface_reconcile", Recover: reconcileInterfaces},
		{Component: "journal_final", Recover: verifyRecoveryJournal},
	}}
	recoveryTracker := &reliability.Tracker{}
	startupReport, startupErr := recoveryCoordinator.Run(ctx)
	recoveryTracker.Record(startupReport)
	if startupErr != nil {
		logger.ErrorContext(ctx, "startup recovery degraded", "component", startupReport.FailedComponent)
	}
	if err := startupLease.Release(); err != nil {
		return fmt.Errorf("release startup recovery lock: %w", err)
	}
	mutate := func(ctx context.Context, transactionID domain.ID, component string, action func() (any, error)) (any, error) {
		return withMutationLock(mutationLock, transactionID, component, func() (any, error) {
			if !recoveryTracker.Ready() {
				return nil, reliability.RecoveryRequiredError{}
			}
			unfinished, err := store.UnfinishedOperations(ctx, 1)
			if err != nil {
				return nil, err
			}
			if len(unfinished) != 0 {
				return nil, reliability.RecoveryRequiredError{}
			}
			return action()
		})
	}
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
		status := "ok"
		if !recoveryTracker.Ready() {
			status = "degraded"
		}
		return map[string]string{"status": status, "version": buildinfo.Version}, nil
	}); err != nil {
		return err
	}
	if err := server.Handle(ipc.OperationRecoveryStatus, func(ctx context.Context, payload json.RawMessage) (any, error) {
		if err := decodeEmptyPayload(payload); err != nil {
			return nil, err
		}
		status, err := reliability.Inspect(ctx, store, mutationLock, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		status.LastRecovery = recoveryTracker.Last()
		if !recoveryTracker.Ready() {
			status.Ready = false
			status.RecoveryRequired = true
		}
		return status, nil
	}); err != nil {
		return err
	}
	if err := server.Handle(ipc.OperationRecoveryRun, func(ctx context.Context, payload json.RawMessage) (any, error) {
		if err := decodeEmptyPayload(payload); err != nil {
			return nil, err
		}
		return withMutationLock(mutationLock, domain.ID(logging.OperationID(ctx)), "recovery", func() (any, error) {
			report, recoveryErr := recoveryCoordinator.Run(ctx)
			recoveryTracker.Record(report)
			if recoveryErr != nil {
				logger.ErrorContext(ctx, "manual recovery failed", "component", report.FailedComponent)
			}
			return report, nil
		})
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
		return mutate(ctx, request.TransactionID, "nat", func() (any, error) {
			plan, err := buildPlan(ctx, nat.PlanRequest{Family: request.Family, Forwards: request.Forwards})
			if err != nil {
				return nil, err
			}
			if err := executor.Execute(ctx, request.TransactionID, request.RequestedChange, plan); err != nil {
				return nil, err
			}
			return nat.ApplyResponse{TransactionID: request.TransactionID, State: domain.TransactionCommitted}, nil
		})
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
		return mutate(ctx, request.TransactionID, "haproxy", func() (any, error) {
			plan, err := buildHAProxyPlan(ctx, managedHAProxy.PlanRequest{Frontends: request.Frontends, Backends: request.Backends})
			if err != nil {
				return nil, err
			}
			return haproxyExecutor.Execute(ctx, request.TransactionID, request.RequestedChange, plan)
		})
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

	var outboundMutex sync.Mutex
	singboxTester := managedSingBox.Tester{
		Validator: managedSingBox.NativeValidator{Runner: runner, TempDirectory: configuration.DataDirectory, Timeout: 5 * time.Second},
		Dialer:    &net.Dialer{Timeout: 2 * time.Second},
		Probe:     managedSingBox.LocalProxyProbe{TempDirectory: configuration.DataDirectory, Timeout: 10 * time.Second},
		Timeout:   15 * time.Second,
	}
	loadSingBoxPlan := func(ctx context.Context) (managedSingBox.ExecutionPlan, error) {
		stored, err := store.ListOutbounds(ctx, "", managedSingBox.MaximumOutbounds+1)
		if err != nil {
			return managedSingBox.ExecutionPlan{}, err
		}
		if len(stored) > managedSingBox.MaximumOutbounds {
			return managedSingBox.ExecutionPlan{}, fmt.Errorf("sing-box desired state exceeds %d outbounds", managedSingBox.MaximumOutbounds)
		}
		outbounds := make([]domain.Outbound, 0, len(stored))
		credentials := make(map[domain.ID][]byte)
		for _, item := range stored {
			outbounds = append(outbounds, item.Outbound)
			if item.Outbound.Enabled {
				document, credentialErr := store.OutboundCredential(ctx, item.Outbound.ID)
				if credentialErr != nil {
					return managedSingBox.ExecutionPlan{}, credentialErr
				}
				credentials[item.Outbound.ID] = document
			}
		}
		state, err := managedSingBox.InspectState(configuration.SingBoxConfigPath)
		if err != nil {
			return managedSingBox.ExecutionPlan{}, err
		}
		routeState, err := routing.InspectState(configuration.RoutingStatePath)
		if err != nil {
			return managedSingBox.ExecutionPlan{}, err
		}
		return managedSingBox.BuildRoutedPlan(outbounds, credentials, routeState.Routes, state)
	}
	if err := server.Handle(ipc.OperationSingBoxImport, func(ctx context.Context, payload json.RawMessage) (any, error) {
		var request managedSingBox.ImportRequest
		if err := decodePayload(payload, &request); err != nil {
			return nil, err
		}
		outboundMutex.Lock()
		defer outboundMutex.Unlock()
		parsed, err := managedSingBox.ParseImport(request.Input)
		if err != nil {
			return nil, err
		}
		inputs := make([]database.NewOutbound, 0, len(parsed))
		for _, item := range parsed {
			inputs = append(inputs, database.NewOutbound{Outbound: item.Outbound, CredentialDocument: item.CredentialDocument})
		}
		stored, err := store.CreateOutbounds(ctx, inputs, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		response := managedSingBox.ImportResponse{Outbounds: make([]domain.Outbound, 0, len(stored))}
		for _, item := range stored {
			response.Outbounds = append(response.Outbounds, item.Outbound)
		}
		return response, nil
	}); err != nil {
		return err
	}
	if err := server.Handle(ipc.OperationSingBoxTest, func(ctx context.Context, payload json.RawMessage) (any, error) {
		var request managedSingBox.TestRequest
		if err := decodePayload(payload, &request); err != nil {
			return nil, err
		}
		outboundMutex.Lock()
		defer outboundMutex.Unlock()
		if (request.ID == "") == (request.Input == "") {
			return nil, fmt.Errorf("exactly one stored outbound ID or import input is required")
		}
		if request.ID != "" {
			if request.ExpectedRevision < 1 {
				return nil, fmt.Errorf("expected revision is required")
			}
			stored, err := store.Outbound(ctx, request.ID)
			if err != nil {
				return nil, err
			}
			if stored.Revision != request.ExpectedRevision {
				return nil, database.ErrConflict
			}
			credential, err := store.OutboundCredential(ctx, request.ID)
			if err != nil {
				return nil, err
			}
			health := singboxTester.Test(ctx, stored.Outbound, credential)
			stored.Outbound.Health = health
			updated, err := store.UpdateOutbound(ctx, stored.Outbound, request.ExpectedRevision, nil, time.Now().UTC())
			if err != nil {
				return nil, err
			}
			return managedSingBox.TestResponse{Results: []managedSingBox.TestResult{{Outbound: updated.Outbound, Health: health}}}, nil
		}
		parsed, err := managedSingBox.ParseImport(request.Input)
		if err != nil {
			return nil, err
		}
		if len(parsed) != 1 {
			return nil, fmt.Errorf("connection test requires exactly one outbound")
		}
		health := singboxTester.Test(ctx, parsed[0].Outbound, parsed[0].CredentialDocument)
		return managedSingBox.TestResponse{Results: []managedSingBox.TestResult{{Outbound: parsed[0].Outbound, Health: health}}}, nil
	}); err != nil {
		return err
	}
	if err := server.Handle(ipc.OperationSingBoxPlan, func(ctx context.Context, payload json.RawMessage) (any, error) {
		if err := decodeEmptyPayload(payload); err != nil {
			return nil, err
		}
		outboundMutex.Lock()
		defer outboundMutex.Unlock()
		plan, err := loadSingBoxPlan(ctx)
		if err != nil {
			return nil, err
		}
		return plan.Plan, nil
	}); err != nil {
		return err
	}
	if err := server.Handle(ipc.OperationSingBoxApply, func(ctx context.Context, payload json.RawMessage) (any, error) {
		var request managedSingBox.ApplyRequest
		if err := decodePayload(payload, &request); err != nil {
			return nil, err
		}
		outboundMutex.Lock()
		defer outboundMutex.Unlock()
		return mutate(ctx, request.TransactionID, "singbox", func() (any, error) {
			plan, err := loadSingBoxPlan(ctx)
			if err != nil {
				return nil, err
			}
			if request.ExpectedStateHash == "" || request.ExpectedCandidateHash == "" || plan.Plan.StateHash != request.ExpectedStateHash || plan.Plan.CandidateHash != request.ExpectedCandidateHash {
				return nil, managedSingBox.ErrStateChanged
			}
			return singboxExecutor.Execute(ctx, request.TransactionID, plan)
		})
	}); err != nil {
		return err
	}
	if err := server.Handle(ipc.OperationInterfaceImport, func(ctx context.Context, payload json.RawMessage) (any, error) {
		var request managedInterface.ImportRequest
		if err := decodePayload(payload, &request); err != nil {
			return nil, err
		}
		outboundMutex.Lock()
		defer outboundMutex.Unlock()
		parsed, err := managedInterface.ParseImport(request.Input)
		if err != nil {
			return nil, err
		}
		stored, err := store.CreateOutbound(ctx, parsed.Outbound, parsed.CredentialDocument, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		return managedInterface.ImportResponse{Outbound: stored.Outbound}, nil
	}); err != nil {
		return err
	}
	if err := server.Handle(ipc.OperationInterfaceTest, func(ctx context.Context, payload json.RawMessage) (any, error) {
		var request managedInterface.TestRequest
		if err := decodePayload(payload, &request); err != nil {
			return nil, err
		}
		outboundMutex.Lock()
		defer outboundMutex.Unlock()
		if (request.ID == "") == (request.Input == "") {
			return nil, fmt.Errorf("exactly one stored interface outbound ID or import input is required")
		}
		state, err := managedInterface.InspectState(configuration.InterfaceStatePath, configuration.InterfaceRuntimeDirectory)
		if err != nil {
			return nil, err
		}
		tester := managedInterface.Tester{Runner: runner, RuntimeDirectory: configuration.InterfaceRuntimeDirectory, Timeout: 10 * time.Second}
		if request.ID != "" {
			if request.ExpectedRevision < 1 {
				return nil, fmt.Errorf("expected revision is required")
			}
			stored, err := store.Outbound(ctx, request.ID)
			if err != nil {
				return nil, err
			}
			if stored.Revision != request.ExpectedRevision {
				return nil, database.ErrConflict
			}
			if stored.Outbound.Adapter != domain.OutboundAdapterInterface {
				return nil, fmt.Errorf("outbound does not use the interface adapter")
			}
			credential, err := store.OutboundCredential(ctx, request.ID)
			if err != nil {
				return nil, err
			}
			health := tester.Test(ctx, stored.Outbound, credential, state)
			stored.Outbound.Health = health
			updated, err := store.UpdateOutbound(ctx, stored.Outbound, request.ExpectedRevision, nil, time.Now().UTC())
			if err != nil {
				return nil, err
			}
			return managedInterface.TestResponse{Outbound: updated.Outbound, Health: health}, nil
		}
		parsed, err := managedInterface.ParseImport(request.Input)
		if err != nil {
			return nil, err
		}
		health := tester.Test(ctx, parsed.Outbound, parsed.CredentialDocument, state)
		return managedInterface.TestResponse{Outbound: parsed.Outbound, Health: health}, nil
	}); err != nil {
		return err
	}
	if err := server.Handle(ipc.OperationInterfacePlan, func(ctx context.Context, payload json.RawMessage) (any, error) {
		if err := decodeEmptyPayload(payload); err != nil {
			return nil, err
		}
		outboundMutex.Lock()
		defer outboundMutex.Unlock()
		plan, err := loadInterfacePlan(ctx)
		if err != nil {
			return nil, err
		}
		return plan.Review, nil
	}); err != nil {
		return err
	}
	if err := server.Handle(ipc.OperationInterfaceApply, func(ctx context.Context, payload json.RawMessage) (any, error) {
		var request managedInterface.ApplyRequest
		if err := decodePayload(payload, &request); err != nil {
			return nil, err
		}
		outboundMutex.Lock()
		defer outboundMutex.Unlock()
		return mutate(ctx, request.TransactionID, "interface", func() (any, error) {
			plan, err := loadInterfacePlan(ctx)
			if err != nil {
				return nil, err
			}
			if request.ExpectedStateHash == "" || request.ExpectedCandidateHash == "" || plan.Review.StateHash != request.ExpectedStateHash || plan.Review.CandidateHash != request.ExpectedCandidateHash {
				return nil, managedInterface.ErrStateChanged
			}
			return interfaceExecutor.Execute(ctx, request.TransactionID, plan)
		})
	}); err != nil {
		return err
	}
	loadRoutePlan := func(ctx context.Context) (routeengine.Plan, error) {
		storedOutbounds, err := store.ListOutbounds(ctx, "", managedSingBox.MaximumOutbounds+1)
		if err != nil {
			return routeengine.Plan{}, err
		}
		if len(storedOutbounds) > managedSingBox.MaximumOutbounds {
			return routeengine.Plan{}, fmt.Errorf("sing-box desired state exceeds %d outbounds", managedSingBox.MaximumOutbounds)
		}
		outbounds := make([]domain.Outbound, 0, len(storedOutbounds))
		credentials := make(map[domain.ID][]byte)
		for _, item := range storedOutbounds {
			outbounds = append(outbounds, item.Outbound)
			if item.Outbound.Enabled {
				document, credentialErr := store.OutboundCredential(ctx, item.Outbound.ID)
				if credentialErr != nil {
					return routeengine.Plan{}, credentialErr
				}
				credentials[item.Outbound.ID] = document
			}
		}
		storedRoutes, err := store.ListRoutes(ctx, "", routing.MaximumRoutes+1)
		if err != nil {
			return routeengine.Plan{}, err
		}
		if len(storedRoutes) > routing.MaximumRoutes {
			return routeengine.Plan{}, fmt.Errorf("routing desired state exceeds %d routes", routing.MaximumRoutes)
		}
		routes := make([]domain.Route, 0, len(storedRoutes))
		referenced := make(map[domain.ID]struct{})
		for _, item := range storedRoutes {
			routes = append(routes, item.Route)
			if item.Route.Enabled {
				referenced[item.Route.OutboundID] = struct{}{}
				if item.Route.FallbackOutboundID != "" {
					referenced[item.Route.FallbackOutboundID] = struct{}{}
				}
			}
		}
		resolved := make(routing.ResolvedEndpoints)
		for _, outbound := range outbounds {
			if _, needed := referenced[outbound.ID]; !needed {
				continue
			}
			if _, parseErr := netip.ParseAddr(outbound.Server.Host); parseErr == nil {
				continue
			}
			addresses, resolveErr := net.DefaultResolver.LookupNetIP(ctx, "ip", outbound.Server.Host)
			if resolveErr != nil {
				return routeengine.Plan{}, fmt.Errorf("resolve outbound endpoint %q: %w", outbound.ID, resolveErr)
			}
			if len(addresses) > 16 {
				return routeengine.Plan{}, fmt.Errorf("outbound endpoint %q resolved to more than 16 addresses", outbound.ID)
			}
			resolved[outbound.ID] = addresses
		}
		host, err := collector.Collect(ctx)
		if err != nil {
			return routeengine.Plan{}, err
		}
		routingState, err := routing.InspectState(configuration.RoutingStatePath)
		if err != nil {
			return routeengine.Plan{}, err
		}
		interfaceState, err := managedInterface.InspectState(configuration.InterfaceStatePath, configuration.InterfaceRuntimeDirectory)
		if err != nil {
			return routeengine.Plan{}, err
		}
		interfacePlan, err := managedInterface.BuildPlan(outbounds, credentials, interfaceState, host.Interfaces)
		if err != nil {
			return routeengine.Plan{}, err
		}
		if interfacePlan.Review.StateHash != interfacePlan.Review.CandidateHash {
			return routeengine.Plan{}, fmt.Errorf("native interface lifecycle must be applied before routed traffic can move")
		}
		interfaceOutbounds := make(map[domain.ID]string, len(interfaceState.Entries))
		for _, entry := range interfaceState.Entries {
			interfaceOutbounds[entry.ID] = entry.InterfaceName
		}
		desired, err := routing.BuildPlan(routing.Settings{TableBase: 20000, RulePriorityBase: 21000, ProtectedLocalPrefixes: configuration.ProtectedManagementCIDRs, InterfaceOutbounds: interfaceOutbounds}, routes, outbounds, host, resolved, routingState)
		if err != nil {
			return routeengine.Plan{}, err
		}
		singBoxState, err := managedSingBox.InspectState(configuration.SingBoxConfigPath)
		if err != nil {
			return routeengine.Plan{}, err
		}
		parsedDesired, err := routing.ParseState(desired.Candidate(), true)
		if err != nil {
			return routeengine.Plan{}, err
		}
		singBoxPlan, err := managedSingBox.BuildRoutedPlan(outbounds, credentials, parsedDesired.Routes, singBoxState)
		if err != nil {
			return routeengine.Plan{}, err
		}
		tableExists, err := routing.InspectOwnedTable(ctx, runner, 2*time.Second)
		if err != nil {
			return routeengine.Plan{}, err
		}
		native, err := routing.BuildNativePlan(desired, tableExists)
		if err != nil {
			return routeengine.Plan{}, err
		}
		return routeengine.BuildPlan(singBoxPlan, desired, native, interfaceState)
	}
	if err := server.Handle(ipc.OperationRoutesPlan, func(ctx context.Context, payload json.RawMessage) (any, error) {
		if err := decodeEmptyPayload(payload); err != nil {
			return nil, err
		}
		outboundMutex.Lock()
		defer outboundMutex.Unlock()
		plan, err := loadRoutePlan(ctx)
		if err != nil {
			return nil, err
		}
		return plan.Review, nil
	}); err != nil {
		return err
	}
	if err := server.Handle(ipc.OperationRoutesApply, func(ctx context.Context, payload json.RawMessage) (any, error) {
		var request routeengine.ApplyRequest
		if err := decodePayload(payload, &request); err != nil {
			return nil, err
		}
		outboundMutex.Lock()
		defer outboundMutex.Unlock()
		return mutate(ctx, request.TransactionID, "routing", func() (any, error) {
			plan, err := loadRoutePlan(ctx)
			if err != nil {
				return nil, err
			}
			review := plan.Review
			if request.ExpectedSingBoxStateHash == "" || request.ExpectedRoutingStateHash == "" || request.ExpectedInterfaceStateHash == "" || request.ExpectedSingBoxCandidate == "" || request.ExpectedRoutingCandidate == "" || request.ExpectedNativeCandidate == "" || request.ExpectedCombinedCandidate == "" ||
				review.SingBoxStateHash != request.ExpectedSingBoxStateHash || review.RoutingStateHash != request.ExpectedRoutingStateHash || review.InterfaceStateHash != request.ExpectedInterfaceStateHash || review.SingBoxCandidateHash != request.ExpectedSingBoxCandidate || review.RoutingCandidateHash != request.ExpectedRoutingCandidate || review.NativeCandidateHash != request.ExpectedNativeCandidate || review.CombinedCandidateHash != request.ExpectedCombinedCandidate {
				return nil, routeengine.ErrStateChanged
			}
			return routeExecutor.Execute(ctx, request.TransactionID, plan)
		})
	}); err != nil {
		return err
	}

	var xrayMutex sync.Mutex
	loadXrayPlan := func(ctx context.Context) (managedXray.FragmentExecutionPlan, managedXray.Installation, error) {
		report, err := xrayDiscoverer.Discover(ctx)
		if err != nil {
			return managedXray.FragmentExecutionPlan{}, managedXray.Installation{}, err
		}
		installation, err := selectManagedXrayInstallation(report)
		if err != nil {
			return managedXray.FragmentExecutionPlan{}, managedXray.Installation{}, err
		}
		snapshot, err := managedXray.SnapshotConfdir(installation)
		if err != nil {
			return managedXray.FragmentExecutionPlan{}, managedXray.Installation{}, err
		}
		stored, err := store.ListXrayBindings(ctx, "", managedXray.MaximumBindings+1)
		if err != nil {
			return managedXray.FragmentExecutionPlan{}, managedXray.Installation{}, err
		}
		if len(stored) > managedXray.MaximumBindings {
			return managedXray.FragmentExecutionPlan{}, managedXray.Installation{}, fmt.Errorf("Xray desired state exceeds %d bindings", managedXray.MaximumBindings)
		}
		bindings := make([]domain.XrayBinding, 0, len(stored))
		for _, item := range stored {
			bindings = append(bindings, item.Binding)
		}
		installation = snapshot.InstallationWithEffectiveTags(installation)
		plan, err := managedXray.BuildFragmentPlan(installation, bindings, snapshot.Foreign, snapshot.Fragment)
		return plan, installation, err
	}
	if err := server.Handle(ipc.OperationXrayDiscover, func(ctx context.Context, payload json.RawMessage) (any, error) {
		if err := decodeEmptyPayload(payload); err != nil {
			return nil, err
		}
		xrayMutex.Lock()
		defer xrayMutex.Unlock()
		return xrayDiscoverer.Discover(ctx)
	}); err != nil {
		return err
	}
	if err := server.Handle(ipc.OperationXrayPlan, func(ctx context.Context, payload json.RawMessage) (any, error) {
		if err := decodeEmptyPayload(payload); err != nil {
			return nil, err
		}
		xrayMutex.Lock()
		defer xrayMutex.Unlock()
		plan, _, err := loadXrayPlan(ctx)
		if err != nil {
			return nil, err
		}
		return plan.Review, nil
	}); err != nil {
		return err
	}
	if err := server.Handle(ipc.OperationXrayApply, func(ctx context.Context, payload json.RawMessage) (any, error) {
		var request managedXray.FragmentApplyRequest
		if err := decodePayload(payload, &request); err != nil {
			return nil, err
		}
		xrayMutex.Lock()
		defer xrayMutex.Unlock()
		return mutate(ctx, request.TransactionID, "xray", func() (any, error) {
			plan, installation, err := loadXrayPlan(ctx)
			if err != nil {
				return nil, err
			}
			if request.ExpectedForeignStateHash == "" || request.ExpectedFragmentStateHash == "" || request.ExpectedCandidateHash == "" ||
				plan.Review.ForeignStateHash != request.ExpectedForeignStateHash || plan.Review.FragmentStateHash != request.ExpectedFragmentStateHash || plan.Review.CandidateHash != request.ExpectedCandidateHash {
				return nil, managedXray.ErrXrayStateChanged
			}
			executor := managedXray.FragmentExecutor{Runner: runner, Journal: store, Protector: protector, Installation: installation}
			return executor.Execute(ctx, request.TransactionID, plan)
		})
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

func withMutationLock(lock reliability.FileLock, operationID domain.ID, component string, action func() (any, error)) (value any, err error) {
	lease, err := lock.TryAcquire(operationID, component)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, lease.Release())
	}()
	return action()
}

func selectManagedXrayInstallation(report managedXray.Report) (managedXray.Installation, error) {
	var selected []managedXray.Installation
	for _, installation := range report.Installations {
		if installation.MutationStrategy == managedXray.ManagedFragment {
			selected = append(selected, installation)
		}
	}
	if len(selected) == 0 {
		return managedXray.Installation{}, fmt.Errorf("no Xray installation has a proven managed-fragment boundary")
	}
	if len(selected) != 1 {
		return managedXray.Installation{}, fmt.Errorf("multiple writable Xray installations are ambiguous")
	}
	return selected[0], nil
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
