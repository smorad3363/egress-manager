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
	"github.com/egress-manager/egress-manager/internal/inventory"
	"github.com/egress-manager/egress-manager/internal/ipc"
	"github.com/egress-manager/egress-manager/internal/nat"
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
	store := database.NewStore(databaseConnection)
	runner := system.ExecRunner{}
	executor := nat.Executor{Runner: runner, Journal: store, Verifier: nat.SystemVerifier{Runner: runner}}
	if err := executor.Recover(ctx); err != nil {
		return fmt.Errorf("recover interrupted network operations: %w", err)
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
		tableExists, err := nat.InspectOwnedTable(ctx, runner, request.Family, 2*time.Second)
		if err != nil {
			return nat.Plan{}, err
		}
		return nat.BuildNFTPlan(request.Family, request.Forwards, hostInventory.Listeners, nat.SafetyPolicy{
			SSHPorts:               sshPorts,
			PanelPort:              configuration.ListenPort,
			ProtectedLocalPrefixes: configuration.ProtectedManagementCIDRs,
		}, tableExists)
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
		return (nat.CounterReader{Runner: runner}).Read(ctx, request.Family)
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
