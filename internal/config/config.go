// Package config loads and persists Egress Manager process configuration.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultListenAddress = "127.0.0.1"
	defaultCookieName    = "egress_session"
)

type Config struct {
	ListenAddress            string         `json:"listen_address"`
	ListenPort               uint16         `json:"listen_port"`
	DataDirectory            string         `json:"data_directory"`
	DatabasePath             string         `json:"database_path"`
	ControlSocketPath        string         `json:"control_socket_path"`
	SessionCookieName        string         `json:"session_cookie_name"`
	SSHPorts                 []uint16       `json:"ssh_ports"`
	ProtectedManagementCIDRs []netip.Prefix `json:"protected_management_cidrs"`
	TLSCertificatePath       string         `json:"tls_certificate_path,omitempty"`
	TLSPrivateKeyPath        string         `json:"tls_private_key_path,omitempty"`
}

func Default(dataDirectory string) Config {
	return Config{
		ListenAddress:            defaultListenAddress,
		DataDirectory:            dataDirectory,
		DatabasePath:             filepath.Join(dataDirectory, "egress-manager.db"),
		ControlSocketPath:        filepath.Join(dataDirectory, "egressd.sock"),
		SessionCookieName:        defaultCookieName,
		SSHPorts:                 []uint16{22},
		ProtectedManagementCIDRs: []netip.Prefix{},
	}
}

func (configuration Config) Validate() error {
	var errs []error
	address, err := netip.ParseAddr(configuration.ListenAddress)
	if err != nil || !address.IsValid() {
		errs = append(errs, fmt.Errorf("listen_address must be an IP address"))
	}
	if configuration.ListenPort == 0 {
		errs = append(errs, fmt.Errorf("listen_port must be selected before use"))
	}
	if len(configuration.SSHPorts) == 0 {
		errs = append(errs, fmt.Errorf("at least one SSH port is required"))
	}
	protectedPorts := map[uint16]struct{}{configuration.ListenPort: {}}
	for _, port := range configuration.SSHPorts {
		if port == 0 {
			errs = append(errs, fmt.Errorf("SSH ports must be nonzero"))
		}
		if _, exists := protectedPorts[port]; exists {
			errs = append(errs, fmt.Errorf("protected port %d is duplicated", port))
		}
		protectedPorts[port] = struct{}{}
	}
	for _, prefix := range configuration.ProtectedManagementCIDRs {
		if !prefix.IsValid() || prefix != prefix.Masked() {
			errs = append(errs, fmt.Errorf("protected_management_cidrs must contain canonical prefixes"))
		}
	}
	if strings.TrimSpace(configuration.DataDirectory) == "" || !filepath.IsAbs(configuration.DataDirectory) {
		errs = append(errs, fmt.Errorf("data_directory must be absolute"))
	}
	if strings.TrimSpace(configuration.DatabasePath) == "" || !filepath.IsAbs(configuration.DatabasePath) {
		errs = append(errs, fmt.Errorf("database_path must be absolute"))
	}
	if strings.TrimSpace(configuration.ControlSocketPath) == "" || !filepath.IsAbs(configuration.ControlSocketPath) {
		errs = append(errs, fmt.Errorf("control_socket_path must be absolute"))
	}
	if (configuration.TLSCertificatePath == "") != (configuration.TLSPrivateKeyPath == "") {
		errs = append(errs, fmt.Errorf("TLS certificate and private key paths must be configured together"))
	}
	if configuration.TLSCertificatePath != "" && (!filepath.IsAbs(configuration.TLSCertificatePath) || !filepath.IsAbs(configuration.TLSPrivateKeyPath)) {
		errs = append(errs, fmt.Errorf("TLS certificate and private key paths must be absolute"))
	}
	if address.IsValid() && !address.IsLoopback() && configuration.TLSCertificatePath == "" {
		errs = append(errs, fmt.Errorf("non-loopback listen_address requires TLS"))
	}
	if configuration.SessionCookieName == "" || len(configuration.SessionCookieName) > 64 {
		errs = append(errs, fmt.Errorf("session_cookie_name must be 1 to 64 bytes"))
	}
	for _, r := range configuration.SessionCookieName {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			errs = append(errs, fmt.Errorf("session_cookie_name contains an unsafe character"))
			break
		}
	}
	return errors.Join(errs...)
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read configuration: %w", err)
	}
	var configuration Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&configuration); err != nil {
		return Config{}, fmt.Errorf("decode configuration: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Config{}, fmt.Errorf("decode configuration: trailing data")
	}
	if err := configuration.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate configuration: %w", err)
	}
	return configuration, nil
}

func Save(path string, configuration Config) error {
	if err := configuration.Validate(); err != nil {
		return fmt.Errorf("validate configuration: %w", err)
	}
	data, err := json.MarshalIndent(configuration, "", "  ")
	if err != nil {
		return fmt.Errorf("encode configuration: %w", err)
	}
	data = append(data, '\n')

	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create configuration directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".egress-config-*")
	if err != nil {
		return fmt.Errorf("create temporary configuration: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = os.Remove(temporaryPath)
	}
	defer cleanup()

	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set configuration permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write configuration: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync configuration: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close configuration: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace configuration atomically: %w", err)
	}
	return nil
}
