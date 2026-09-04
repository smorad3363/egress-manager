package haproxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
)

const maximumRuntimeResponse = 1 << 20

type RuntimeDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type NetDialer struct{ Dialer net.Dialer }

func (dialer NetDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return dialer.Dialer.DialContext(ctx, network, address)
}

type RuntimeClient struct {
	SocketPath string
	Dialer     RuntimeDialer
	Timeout    time.Duration
}

type RuntimeInfo struct {
	Name               string `json:"name"`
	Version            string `json:"version"`
	PID                uint64 `json:"pid"`
	UptimeSeconds      uint64 `json:"uptime_seconds"`
	CurrentConnections uint64 `json:"current_connections"`
	TotalConnections   uint64 `json:"total_connections"`
}

type RuntimeStat struct {
	FrontendID       domain.ID           `json:"frontend_id"`
	BackendID        domain.ID           `json:"backend_id,omitempty"`
	Kind             string              `json:"kind"`
	Status           domain.HealthStatus `json:"status"`
	CurrentSessions  uint64              `json:"current_sessions"`
	MaximumSessions  uint64              `json:"maximum_sessions"`
	TotalSessions    uint64              `json:"total_sessions"`
	BytesIn          uint64              `json:"bytes_in"`
	BytesOut         uint64              `json:"bytes_out"`
	ConnectionErrors uint64              `json:"connection_errors"`
	ResponseErrors   uint64              `json:"response_errors"`
	CheckFailures    uint64              `json:"check_failures"`
	DowntimeSeconds  uint64              `json:"downtime_seconds"`
}

type RuntimeSnapshot struct {
	Info  RuntimeInfo   `json:"info"`
	Stats []RuntimeStat `json:"stats"`
}

func (client RuntimeClient) Snapshot(ctx context.Context) (RuntimeSnapshot, error) {
	infoOutput, err := client.query(ctx, "show info")
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	info, err := parseRuntimeInfo(infoOutput)
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	statOutput, err := client.query(ctx, "show stat")
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	stats, err := parseRuntimeStats(statOutput)
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	return RuntimeSnapshot{Info: info, Stats: stats}, nil
}

func (client RuntimeClient) query(ctx context.Context, command string) ([]byte, error) {
	if client.Dialer == nil || !strings.HasPrefix(client.SocketPath, "/") {
		return nil, fmt.Errorf("HAProxy Runtime API client is not configured")
	}
	if command == "" || len(command) > 64 || strings.ContainsAny(command, "\r\n\x00") {
		return nil, fmt.Errorf("HAProxy Runtime API command is invalid")
	}
	timeout := client.Timeout
	if timeout <= 0 || timeout > 10*time.Second {
		timeout = 2 * time.Second
	}
	queryContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	connection, err := client.Dialer.DialContext(queryContext, "unix", client.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("connect HAProxy Runtime API: %w", err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(timeout))
	if _, err := io.WriteString(connection, command+"\nquit\n"); err != nil {
		return nil, fmt.Errorf("write HAProxy Runtime API command: %w", err)
	}
	reader := io.LimitReader(connection, maximumRuntimeResponse+1)
	output, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read HAProxy Runtime API response: %w", err)
	}
	if len(output) > maximumRuntimeResponse {
		return nil, fmt.Errorf("HAProxy Runtime API response exceeds %d bytes", maximumRuntimeResponse)
	}
	return output, nil
}

func parseRuntimeInfo(input []byte) (RuntimeInfo, error) {
	values := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(input))
	for scanner.Scan() {
		key, value, found := strings.Cut(scanner.Text(), ":")
		if found {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return RuntimeInfo{}, fmt.Errorf("scan HAProxy info: %w", err)
	}
	info := RuntimeInfo{Name: values["Name"], Version: values["Version"]}
	var errs []error
	info.PID, errs = parseRequiredUint(values, "Pid", errs)
	info.UptimeSeconds, errs = parseRequiredUint(values, "Uptime_sec", errs)
	info.CurrentConnections, errs = parseRequiredUint(values, "CurrConns", errs)
	info.TotalConnections, errs = parseRequiredUint(values, "CumConns", errs)
	if info.Name == "" || info.Version == "" {
		errs = append(errs, fmt.Errorf("HAProxy info identity is missing"))
	}
	if err := errors.Join(errs...); err != nil {
		return RuntimeInfo{}, err
	}
	return info, nil
}

func parseRequiredUint(values map[string]string, key string, errs []error) (uint64, []error) {
	value, err := strconv.ParseUint(values[key], 10, 64)
	if err != nil {
		errs = append(errs, fmt.Errorf("HAProxy info field %s is invalid", key))
	}
	return value, errs
}

func parseRuntimeStats(input []byte) ([]RuntimeStat, error) {
	reader := csv.NewReader(bytes.NewReader(input))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("decode HAProxy statistics: %w", err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("HAProxy statistics are empty")
	}
	headings := records[0]
	if len(headings) > 0 {
		headings[0] = strings.TrimSpace(strings.TrimPrefix(headings[0], "#"))
	}
	indexes := map[string]int{}
	for index, heading := range headings {
		indexes[strings.TrimSpace(heading)] = index
	}
	for _, required := range []string{"pxname", "svname", "scur", "smax", "stot", "bin", "bout", "econ", "eresp", "status", "chkfail", "downtime"} {
		if _, exists := indexes[required]; !exists {
			return nil, fmt.Errorf("HAProxy statistics omit %s", required)
		}
	}
	stats := make([]RuntimeStat, 0)
	for _, record := range records[1:] {
		if len(record) < len(headings) {
			continue
		}
		proxyName := record[indexes["pxname"]]
		serverName := record[indexes["svname"]]
		frontendID, kind, backendID, ok := ownedStatIdentity(proxyName, serverName)
		if !ok {
			continue
		}
		stat := RuntimeStat{FrontendID: frontendID, BackendID: backendID, Kind: kind, Status: runtimeHealth(record[indexes["status"]])}
		fields := []struct {
			name   string
			output *uint64
		}{{"scur", &stat.CurrentSessions}, {"smax", &stat.MaximumSessions}, {"stot", &stat.TotalSessions}, {"bin", &stat.BytesIn}, {"bout", &stat.BytesOut}, {"econ", &stat.ConnectionErrors}, {"eresp", &stat.ResponseErrors}, {"chkfail", &stat.CheckFailures}, {"downtime", &stat.DowntimeSeconds}}
		for _, field := range fields {
			value := record[indexes[field.name]]
			if value == "" {
				continue
			}
			parsed, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("HAProxy statistic %s is invalid", field.name)
			}
			*field.output = parsed
		}
		stats = append(stats, stat)
		if len(stats) > MaximumFrontends*(domain.MaximumHAProxyBackendsPerFrontend+2) {
			return nil, fmt.Errorf("HAProxy statistics exceed owned record limit")
		}
	}
	return stats, nil
}

func ownedStatIdentity(proxyName, serverName string) (domain.ID, string, domain.ID, bool) {
	if strings.HasPrefix(proxyName, "egm_fe_") && serverName == "FRONTEND" {
		id := domain.ID(strings.TrimPrefix(proxyName, "egm_fe_"))
		if id.Validate("HAProxy frontend id") == nil {
			return id, "frontend", "", true
		}
	}
	if strings.HasPrefix(proxyName, "egm_pool_") {
		frontendID := domain.ID(strings.TrimPrefix(proxyName, "egm_pool_"))
		if frontendID.Validate("HAProxy frontend id") != nil {
			return "", "", "", false
		}
		if serverName == "BACKEND" {
			return frontendID, "backend", "", true
		}
		if strings.HasPrefix(serverName, "egm_srv_") {
			backendID := domain.ID(strings.TrimPrefix(serverName, "egm_srv_"))
			if backendID.Validate("HAProxy backend id") == nil {
				return frontendID, "server", backendID, true
			}
		}
	}
	return "", "", "", false
}

func runtimeHealth(status string) domain.HealthStatus {
	status = strings.ToUpper(status)
	switch {
	case strings.HasPrefix(status, "UP"), status == "OPEN":
		return domain.HealthHealthy
	case strings.HasPrefix(status, "DOWN"):
		return domain.HealthUnhealthy
	case strings.HasPrefix(status, "MAINT"):
		return domain.HealthDisabled
	case strings.HasPrefix(status, "NOLB"):
		return domain.HealthDegraded
	default:
		return domain.HealthUnknown
	}
}
