package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/egress-manager/egress-manager/internal/buildinfo"
	"github.com/egress-manager/egress-manager/internal/config"
	"github.com/egress-manager/egress-manager/internal/ipc"
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
	server, err := ipc.NewServer(authenticator, logger)
	if err != nil {
		return err
	}
	if err := server.Handle(ipc.OperationHealth, func(_ context.Context, payload json.RawMessage) (any, error) {
		if err := decodeEmptyPayload(payload); err != nil {
			return nil, err
		}
		return map[string]string{"status": "ok", "version": buildinfo.Version}, nil
	}); err != nil {
		return err
	}
	if err := server.Handle(ipc.OperationInventory, func(_ context.Context, payload json.RawMessage) (any, error) {
		if err := decodeEmptyPayload(payload); err != nil {
			return nil, err
		}
		return map[string]any{"interfaces": []any{}}, nil
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
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var input struct{}
	if err := decoder.Decode(&input); err != nil {
		return fmt.Errorf("decode IPC payload: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("decode IPC payload: trailing data")
	}
	return nil
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
