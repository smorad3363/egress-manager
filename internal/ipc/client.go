package ipc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/egress-manager/egress-manager/internal/logging"
)

type Client struct {
	SocketPath    string
	Authenticator *Authenticator
	Timeout       time.Duration
	Random        io.Reader
	Now           func() time.Time
	Dial          func(context.Context, string, string) (net.Conn, error)
}

func (client Client) Call(ctx context.Context, operation Operation, input any, output any) error {
	if client.Authenticator == nil {
		return fmt.Errorf("IPC authenticator is required")
	}
	if err := operation.Validate(); err != nil {
		return err
	}
	timeout := client.Timeout
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	now := client.Now
	if now == nil {
		now = time.Now
	}
	random := client.Random
	if random == nil {
		random = rand.Reader
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode IPC request payload: %w", err)
	}
	nonce := make([]byte, nonceBytes)
	if _, err := io.ReadFull(random, nonce); err != nil {
		return fmt.Errorf("read IPC nonce: %w", err)
	}
	operationID := logging.OperationID(ctx)
	if !logging.ValidOperationID(operationID) {
		operationID, err = logging.NewOperationID(random)
		if err != nil {
			return err
		}
	}
	request := Request{
		Version:     ProtocolVersion,
		OperationID: operationID,
		Timestamp:   now().UTC().Unix(),
		Nonce:       base64.RawURLEncoding.EncodeToString(nonce),
		Operation:   operation,
		Payload:     payload,
	}
	if err := client.Authenticator.SignRequest(&request); err != nil {
		return err
	}

	dial := client.Dial
	if dial == nil {
		dialer := &net.Dialer{}
		dial = dialer.DialContext
	}
	connection, err := dial(ctx, "unix", client.SocketPath)
	if err != nil {
		return fmt.Errorf("connect to egressd: %w", err)
	}
	defer connection.Close()
	deadline, _ := ctx.Deadline()
	_ = connection.SetDeadline(deadline)
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return fmt.Errorf("write IPC request: %w", err)
	}
	decoder := json.NewDecoder(io.LimitReader(connection, maximumFrameSize+1))
	var response Response
	if err := decoder.Decode(&response); err != nil {
		return fmt.Errorf("read IPC response: %w", err)
	}
	if err := client.Authenticator.VerifyResponse(response, operationID, now().UTC()); err != nil {
		return err
	}
	if !response.Success {
		return &RemoteError{Code: response.ErrorCode, Message: response.Message}
	}
	if output == nil {
		return nil
	}
	if err := json.Unmarshal(response.Payload, output); err != nil {
		return fmt.Errorf("decode IPC response payload: %w", err)
	}
	return nil
}

type RemoteError struct {
	Code    string
	Message string
}

func (remoteError *RemoteError) Error() string {
	return remoteError.Code
}

func IsRemoteError(err error, code string) bool {
	var remoteError *RemoteError
	return errors.As(err, &remoteError) && remoteError.Code == code
}
