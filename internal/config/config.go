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
	ListenAddress     string `json:"listen_address"`
	ListenPort        uint16 `json:"listen_port"`
	DataDirectory     string `json:"data_directory"`
	DatabasePath      string `json:"database_path"`
	ControlSocketPath string `json:"control_socket_path"`
	SessionCookieName string `json:"session_cookie_name"`
}

func Default(dataDirectory string) Config {
	return Config{
		ListenAddress:     defaultListenAddress,
		DataDirectory:     dataDirectory,
		DatabasePath:      filepath.Join(dataDirectory, "egress-manager.db"),
		ControlSocketPath: filepath.Join(dataDirectory, "egressd.sock"),
		SessionCookieName: defaultCookieName,
	}
}

func (configuration Config) Validate() error {
	var errs []error
	address, err := netip.ParseAddr(configuration.ListenAddress)
	if err != nil || !address.IsValid() {
		errs = append(errs, fmt.Errorf("listen_address must be an IP address"))
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
