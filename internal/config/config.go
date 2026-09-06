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
	ListenAddress             string         `json:"listen_address"`
	ListenPort                uint16         `json:"listen_port"`
	DataDirectory             string         `json:"data_directory"`
	DatabasePath              string         `json:"database_path"`
	ControlSocketPath         string         `json:"control_socket_path"`
	OperationLockPath         string         `json:"operation_lock_path"`
	HAProxyConfigPath         string         `json:"haproxy_config_path"`
	HAProxyRuntimeSocketPath  string         `json:"haproxy_runtime_socket_path"`
	HAProxyPIDPath            string         `json:"haproxy_pid_path"`
	SingBoxConfigPath         string         `json:"sing_box_config_path"`
	RoutingStatePath          string         `json:"routing_state_path"`
	BypassStatePath           string         `json:"bypass_state_path"`
	InterfaceStatePath        string         `json:"interface_state_path"`
	InterfaceRuntimeDirectory string         `json:"interface_runtime_directory"`
	SessionCookieName         string         `json:"session_cookie_name"`
	SSHPorts                  []uint16       `json:"ssh_ports"`
	ProtectedManagementCIDRs  []netip.Prefix `json:"protected_management_cidrs"`
	TLSCertificatePath        string         `json:"tls_certificate_path,omitempty"`
	TLSPrivateKeyPath         string         `json:"tls_private_key_path,omitempty"`
}

func Default(dataDirectory string) Config {
	interfaceRuntime := filepath.Join("/run", "egress-manager", "interface-outbounds")
	return Config{
		ListenAddress:             defaultListenAddress,
		DataDirectory:             dataDirectory,
		DatabasePath:              filepath.Join(dataDirectory, "egress-manager.db"),
		ControlSocketPath:         filepath.Join(dataDirectory, "egressd.sock"),
		OperationLockPath:         filepath.Join(dataDirectory, "operation.lock"),
		HAProxyConfigPath:         filepath.Join(dataDirectory, "haproxy.cfg"),
		HAProxyRuntimeSocketPath:  filepath.Join(dataDirectory, "haproxy-runtime.sock"),
		HAProxyPIDPath:            filepath.Join(dataDirectory, "haproxy.pid"),
		SingBoxConfigPath:         filepath.Join(dataDirectory, "sing-box.json"),
		RoutingStatePath:          filepath.Join(dataDirectory, "routing.json"),
		BypassStatePath:           filepath.Join(dataDirectory, "bypass.json"),
		InterfaceStatePath:        filepath.Join(interfaceRuntime, "state.json"),
		InterfaceRuntimeDirectory: interfaceRuntime,
		SessionCookieName:         defaultCookieName,
		SSHPorts:                  []uint16{22},
		ProtectedManagementCIDRs:  []netip.Prefix{},
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
	for name, path := range map[string]string{
		"operation_lock_path":         configuration.OperationLockPath,
		"haproxy_config_path":         configuration.HAProxyConfigPath,
		"haproxy_runtime_socket_path": configuration.HAProxyRuntimeSocketPath,
		"haproxy_pid_path":            configuration.HAProxyPIDPath,
		"sing_box_config_path":        configuration.SingBoxConfigPath,
		"routing_state_path":          configuration.RoutingStatePath,
		"bypass_state_path":           configuration.BypassStatePath,
		"interface_state_path":        configuration.InterfaceStatePath,
		"interface_runtime_directory": configuration.InterfaceRuntimeDirectory,
	} {
		if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
			errs = append(errs, fmt.Errorf("%s must be absolute", name))
		}
	}
	paths := []string{configuration.ControlSocketPath, configuration.OperationLockPath, configuration.HAProxyConfigPath, configuration.HAProxyRuntimeSocketPath, configuration.HAProxyPIDPath, configuration.SingBoxConfigPath, configuration.RoutingStatePath, configuration.BypassStatePath, configuration.InterfaceStatePath, configuration.InterfaceRuntimeDirectory}
	seenPaths := map[string]struct{}{}
	for _, path := range paths {
		cleaned := filepath.Clean(path)
		if _, exists := seenPaths[cleaned]; exists {
			errs = append(errs, fmt.Errorf("runtime paths must be distinct"))
		}
		seenPaths[cleaned] = struct{}{}
	}
	if filepath.Clean(filepath.Dir(configuration.InterfaceStatePath)) != filepath.Clean(configuration.InterfaceRuntimeDirectory) {
		errs = append(errs, fmt.Errorf("interface_state_path must be inside interface_runtime_directory"))
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
	if configuration.RoutingStatePath == "" && filepath.IsAbs(configuration.DataDirectory) {
		configuration.RoutingStatePath = filepath.Join(configuration.DataDirectory, "routing.json")
	}
	if configuration.OperationLockPath == "" && filepath.IsAbs(configuration.DataDirectory) {
		configuration.OperationLockPath = filepath.Join(configuration.DataDirectory, "operation.lock")
	}
	if configuration.BypassStatePath == "" && filepath.IsAbs(configuration.DataDirectory) {
		configuration.BypassStatePath = filepath.Join(configuration.DataDirectory, "bypass.json")
	}
	if configuration.InterfaceRuntimeDirectory == "" {
		configuration.InterfaceRuntimeDirectory = filepath.Join("/run", "egress-manager", "interface-outbounds")
	}
	if configuration.InterfaceStatePath == "" {
		configuration.InterfaceStatePath = filepath.Join(configuration.InterfaceRuntimeDirectory, "state.json")
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
