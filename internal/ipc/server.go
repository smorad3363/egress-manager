package ipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/egress-manager/egress-manager/internal/logging"
)

type Handler func(context.Context, json.RawMessage) (any, error)

type Server struct {
	authenticator *Authenticator
	logger        *slog.Logger
	handlers      map[Operation]Handler
	replay        *replayCache
	now           func() time.Time
}

func NewServer(authenticator *Authenticator, logger *slog.Logger) (*Server, error) {
	if authenticator == nil {
		return nil, fmt.Errorf("IPC authenticator is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		authenticator: authenticator,
		logger:        logger,
		handlers:      make(map[Operation]Handler),
		replay:        newReplayCache(),
		now:           time.Now,
	}, nil
}

func (server *Server) Handle(operation Operation, handler Handler) error {
	if err := operation.Validate(); err != nil {
		return err
	}
	if handler == nil {
		return fmt.Errorf("IPC handler is required")
	}
	if _, exists := server.handlers[operation]; exists {
		return fmt.Errorf("IPC handler already registered for %q", operation)
	}
	server.handlers[operation] = handler
	return nil
}

func ListenUnix(path string) (*net.UnixListener, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("IPC socket path must be absolute")
	}
	if info, err := os.Lstat(path); err == nil {
		return nil, fmt.Errorf("refusing to replace existing IPC path with mode %s", info.Mode())
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect IPC socket path: %w", err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("listen on IPC socket: %w", err)
	}
	if err := os.Chmod(path, 0o660); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("set IPC socket permissions: %w", err)
	}
	return listener, nil
}

func (server *Server) Serve(ctx context.Context, listener net.Listener) error {
	if listener == nil {
		return fmt.Errorf("IPC listener is required")
	}
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept IPC connection: %w", err)
		}
		go server.serveConnection(ctx, connection)
	}
}

func (server *Server) serveConnection(ctx context.Context, connection net.Conn) {
	defer connection.Close()
	_ = connection.SetDeadline(server.now().Add(5 * time.Second))
	frame, err := bufio.NewReader(io.LimitReader(connection, maximumFrameSize+1)).ReadBytes('\n')
	if err != nil || len(frame) > maximumFrameSize {
		server.writeFailure(connection, "", "bad_request", "Invalid IPC request.")
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(frame))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		server.writeFailure(connection, request.OperationID, "bad_request", "Invalid IPC request.")
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		server.writeFailure(connection, request.OperationID, "bad_request", "Invalid IPC request.")
		return
	}
	now := server.now().UTC()
	if err := server.authenticator.VerifyRequest(request, now); err != nil {
		server.writeFailure(connection, request.OperationID, "unauthorized", "IPC authentication failed.")
		return
	}
	if !server.replay.add(request.Nonce, now) {
		server.writeFailure(connection, request.OperationID, "replay", "IPC request rejected.")
		return
	}
	handler, exists := server.handlers[request.Operation]
	if !exists {
		server.writeFailure(connection, request.OperationID, "unsupported_operation", "IPC operation is not supported.")
		return
	}
	requestContext := logging.WithOperationID(ctx, request.OperationID)
	value, err := handler(requestContext, request.Payload)
	if err != nil {
		server.logger.ErrorContext(requestContext, "IPC handler failed", "operation", request.Operation, "handler_error_type", fmt.Sprintf("%T", err))
		code := "operation_failed"
		message := "IPC operation failed."
		var coded interface{ IPCErrorCode() string }
		if errors.As(err, &coded) && coded.IPCErrorCode() == "busy" {
			code = "busy"
			message = "Host mutation is already in progress."
		}
		if errors.As(err, &coded) && coded.IPCErrorCode() == "recovery_required" {
			code = "recovery_required"
			message = "Host recovery is required before mutation."
		}
		if errors.As(err, &coded) && coded.IPCErrorCode() == "bypass_active" {
			code = "bypass_active"
			message = "Emergency bypass is active; run recovery before applying routes."
		}
		server.writeFailure(connection, request.OperationID, code, message)
		return
	}
	payload, err := json.Marshal(value)
	if err != nil {
		server.writeFailure(connection, request.OperationID, "encoding_failed", "IPC response failed.")
		return
	}
	server.writeResponse(connection, Response{
		Version:     ProtocolVersion,
		OperationID: request.OperationID,
		Timestamp:   server.now().UTC().Unix(),
		Success:     true,
		Payload:     payload,
	})
}

func (server *Server) writeFailure(writer io.Writer, operationID, code, message string) {
	if !logging.ValidOperationID(operationID) {
		operationID = "00000000000000000000000000000000"
	}
	server.writeResponse(writer, Response{
		Version:     ProtocolVersion,
		OperationID: operationID,
		Timestamp:   server.now().UTC().Unix(),
		Success:     false,
		ErrorCode:   code,
		Message:     message,
	})
}

func (server *Server) writeResponse(writer io.Writer, response Response) {
	if err := server.authenticator.SignResponse(&response); err != nil {
		return
	}
	_ = json.NewEncoder(writer).Encode(response)
}

type replayCache struct {
	mu      sync.Mutex
	nonces  map[string]time.Time
	maximum int
}

func newReplayCache() *replayCache {
	return &replayCache{nonces: make(map[string]time.Time), maximum: 4096}
}

func (cache *replayCache) add(nonce string, now time.Time) bool {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for value, seenAt := range cache.nonces {
		if seenAt.Before(now.Add(-maximumClockSkew)) {
			delete(cache.nonces, value)
		}
	}
	if _, exists := cache.nonces[nonce]; exists {
		return false
	}
	if len(cache.nonces) >= cache.maximum {
		return false
	}
	cache.nonces[nonce] = now
	return true
}
