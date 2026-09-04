// Package ipc implements authenticated, typed communication with egressd.
package ipc

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/egress-manager/egress-manager/internal/logging"
)

const (
	ProtocolVersion  = 1
	maximumFrameSize = 256 << 10
	maximumClockSkew = 30 * time.Second
	nonceBytes       = 16
)

var (
	ErrAuthentication = errors.New("IPC authentication failed")
	ErrReplay         = errors.New("IPC request replayed")
	ErrUnknownAction  = errors.New("IPC operation is not supported")
)

type Operation string

const (
	OperationHealth          Operation = "health"
	OperationInventory       Operation = "network.inventory"
	OperationNATPlan         Operation = "nat.plan"
	OperationNATApply        Operation = "nat.apply"
	OperationNATCount        Operation = "nat.counters"
	OperationHAProxyPlan     Operation = "haproxy.plan"
	OperationHAProxyApply    Operation = "haproxy.apply"
	OperationHAProxyStats    Operation = "haproxy.stats"
	OperationSingBoxImport   Operation = "singbox.import"
	OperationSingBoxTest     Operation = "singbox.test"
	OperationSingBoxPlan     Operation = "singbox.plan"
	OperationSingBoxApply    Operation = "singbox.apply"
	OperationRoutesPlan      Operation = "routes.plan"
	OperationRoutesApply     Operation = "routes.apply"
	OperationXrayDiscover    Operation = "xray.discover"
	OperationXrayPlan        Operation = "xray.plan"
	OperationXrayApply       Operation = "xray.apply"
	OperationInterfaceImport Operation = "interface.import"
	OperationInterfacePlan   Operation = "interface.plan"
	OperationInterfaceApply  Operation = "interface.apply"
)

func (operation Operation) Validate() error {
	switch operation {
	case OperationHealth, OperationInventory, OperationNATPlan, OperationNATApply, OperationNATCount, OperationHAProxyPlan, OperationHAProxyApply, OperationHAProxyStats, OperationSingBoxImport, OperationSingBoxTest, OperationSingBoxPlan, OperationSingBoxApply, OperationRoutesPlan, OperationRoutesApply, OperationXrayDiscover, OperationXrayPlan, OperationXrayApply, OperationInterfaceImport, OperationInterfacePlan, OperationInterfaceApply:
		return nil
	default:
		return ErrUnknownAction
	}
}

type Request struct {
	Version     int             `json:"version"`
	OperationID string          `json:"operation_id"`
	Timestamp   int64           `json:"timestamp"`
	Nonce       string          `json:"nonce"`
	Operation   Operation       `json:"operation"`
	Payload     json.RawMessage `json:"payload"`
	MAC         string          `json:"mac"`
}

type Response struct {
	Version     int             `json:"version"`
	OperationID string          `json:"operation_id"`
	Timestamp   int64           `json:"timestamp"`
	Success     bool            `json:"success"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	ErrorCode   string          `json:"error_code,omitempty"`
	Message     string          `json:"message,omitempty"`
	MAC         string          `json:"mac"`
}

type Authenticator struct {
	key [32]byte
}

func NewAuthenticator(key [32]byte) (*Authenticator, error) {
	if key == [32]byte{} {
		return nil, fmt.Errorf("IPC key must not be zero")
	}
	return &Authenticator{key: key}, nil
}

func (authenticator *Authenticator) SignRequest(request *Request) error {
	if err := validateRequestShape(*request); err != nil {
		return err
	}
	request.MAC = authenticator.sign(requestSigningBytes(*request))
	return nil
}

func (authenticator *Authenticator) VerifyRequest(request Request, now time.Time) error {
	if err := validateRequestShape(request); err != nil {
		return ErrAuthentication
	}
	requestTime := time.Unix(request.Timestamp, 0)
	if requestTime.Before(now.Add(-maximumClockSkew)) || requestTime.After(now.Add(maximumClockSkew)) {
		return ErrAuthentication
	}
	if !authenticator.verify(request.MAC, requestSigningBytes(request)) {
		return ErrAuthentication
	}
	return nil
}

func (authenticator *Authenticator) SignResponse(response *Response) error {
	if err := validateResponseShape(*response); err != nil {
		return err
	}
	response.MAC = authenticator.sign(responseSigningBytes(*response))
	return nil
}

func (authenticator *Authenticator) VerifyResponse(response Response, operationID string, now time.Time) error {
	if err := validateResponseShape(response); err != nil || response.OperationID != operationID {
		return ErrAuthentication
	}
	responseTime := time.Unix(response.Timestamp, 0)
	if responseTime.Before(now.Add(-maximumClockSkew)) || responseTime.After(now.Add(maximumClockSkew)) {
		return ErrAuthentication
	}
	if !authenticator.verify(response.MAC, responseSigningBytes(response)) {
		return ErrAuthentication
	}
	return nil
}

func validateRequestShape(request Request) error {
	if request.Version != ProtocolVersion || !logging.ValidOperationID(request.OperationID) {
		return ErrAuthentication
	}
	if err := request.Operation.Validate(); err != nil {
		return err
	}
	nonce, err := base64.RawURLEncoding.Strict().DecodeString(request.Nonce)
	if err != nil || len(nonce) != nonceBytes {
		return ErrAuthentication
	}
	if len(request.Payload) > maximumFrameSize/2 || !json.Valid(request.Payload) {
		return fmt.Errorf("IPC payload is invalid")
	}
	return nil
}

func validateResponseShape(response Response) error {
	if response.Version != ProtocolVersion || !logging.ValidOperationID(response.OperationID) {
		return ErrAuthentication
	}
	if len(response.Payload) > maximumFrameSize/2 || (len(response.Payload) > 0 && !json.Valid(response.Payload)) {
		return fmt.Errorf("IPC response payload is invalid")
	}
	if response.Success && (response.ErrorCode != "" || response.Message != "") {
		return fmt.Errorf("successful IPC response contains an error")
	}
	if !response.Success && response.ErrorCode == "" {
		return fmt.Errorf("failed IPC response has no error code")
	}
	return nil
}

func requestSigningBytes(request Request) []byte {
	var output bytes.Buffer
	writeInteger(&output, uint64(request.Version))
	writeString(&output, request.OperationID)
	writeInteger(&output, uint64(request.Timestamp))
	writeString(&output, request.Nonce)
	writeString(&output, string(request.Operation))
	writeBytes(&output, request.Payload)
	return output.Bytes()
}

func responseSigningBytes(response Response) []byte {
	var output bytes.Buffer
	writeInteger(&output, uint64(response.Version))
	writeString(&output, response.OperationID)
	writeInteger(&output, uint64(response.Timestamp))
	if response.Success {
		writeInteger(&output, 1)
	} else {
		writeInteger(&output, 0)
	}
	writeBytes(&output, response.Payload)
	writeString(&output, response.ErrorCode)
	writeString(&output, response.Message)
	return output.Bytes()
}

func writeString(writer io.Writer, value string) {
	writeBytes(writer, []byte(value))
}

func writeBytes(writer io.Writer, value []byte) {
	writeInteger(writer, uint64(len(value)))
	_, _ = writer.Write(value)
}

func writeInteger(writer io.Writer, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = writer.Write(encoded[:])
}

func (authenticator *Authenticator) sign(message []byte) string {
	mac := hmac.New(sha256.New, authenticator.key[:])
	_, _ = mac.Write(message)
	return hex.EncodeToString(mac.Sum(nil))
}

func (authenticator *Authenticator) verify(encoded string, message []byte) bool {
	provided, err := hex.DecodeString(encoded)
	if err != nil || len(provided) != sha256.Size {
		return false
	}
	expected, _ := hex.DecodeString(authenticator.sign(message))
	return subtle.ConstantTimeCompare(provided, expected) == 1
}
