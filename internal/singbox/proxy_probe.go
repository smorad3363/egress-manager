package singbox

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
)

const defaultExternalIPURL = "https://api.ipify.org"

type LocalProxyProbe struct {
	TempDirectory string
	ExternalIPURL string
	Timeout       time.Duration
}

func (probe LocalProxyProbe) Probe(ctx context.Context, outbound domain.Outbound, plan ExecutionPlan) (ConnectivityResult, error) {
	directory := probe.TempDirectory
	if directory == "" {
		directory = os.TempDir()
	}
	if !filepath.IsAbs(directory) {
		return ConnectivityResult{}, fmt.Errorf("sing-box probe directory must be absolute")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return ConnectivityResult{}, fmt.Errorf("reserve sing-box probe listener")
	}
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	_ = listener.Close()
	var configuration map[string]any
	decoder := json.NewDecoder(bytes.NewReader(plan.candidate))
	decoder.UseNumber()
	if err := decoder.Decode(&configuration); err != nil {
		return ConnectivityResult{}, fmt.Errorf("decode sing-box probe candidate")
	}
	configuration["inbounds"] = []any{map[string]any{"type": "mixed", "tag": "egm_probe_in", "listen": "127.0.0.1", "listen_port": port}}
	configuration["route"] = map[string]any{"final": ownedTagPrefix + string(outbound.ID)}
	candidate, err := json.Marshal(configuration)
	if err != nil || len(candidate) > maximumCandidate {
		return ConnectivityResult{}, fmt.Errorf("encode sing-box probe candidate")
	}
	file, err := os.CreateTemp(directory, ".egress-sing-box-probe-*.json")
	if err != nil {
		return ConnectivityResult{}, fmt.Errorf("create sing-box probe configuration")
	}
	path := file.Name()
	defer os.Remove(path)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return ConnectivityResult{}, fmt.Errorf("secure sing-box probe configuration")
	}
	if _, err := file.Write(candidate); err != nil {
		_ = file.Close()
		return ConnectivityResult{}, fmt.Errorf("write sing-box probe configuration")
	}
	if err := file.Close(); err != nil {
		return ConnectivityResult{}, fmt.Errorf("close sing-box probe configuration")
	}
	processContext, stopProcess := context.WithCancel(ctx)
	command := exec.CommandContext(processContext, "sing-box", "run", "-c", path)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		stopProcess()
		return ConnectivityResult{}, fmt.Errorf("start sing-box probe process")
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	defer func() {
		stopProcess()
		select {
		case <-waited:
		case <-time.After(time.Second):
			if command.Process != nil {
				_ = command.Process.Kill()
			}
		}
	}()
	proxyAddress := net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port)))
	if err := waitForTCP(processContext, proxyAddress, waited, probe.timeout()); err != nil {
		return ConnectivityResult{}, err
	}
	externalURL := probe.ExternalIPURL
	if externalURL == "" {
		externalURL = defaultExternalIPURL
	}
	started := time.Now()
	externalIP, err := queryExternalIP(processContext, proxyAddress, externalURL, probe.timeout())
	if err != nil {
		return ConnectivityResult{}, fmt.Errorf("probe internet through sing-box outbound")
	}
	return ConnectivityResult{ExternalIP: externalIP, TCP: domain.ProbePassed, UDP: domain.ProbeUntestable, Latency: time.Since(started)}, nil
}

func waitForTCP(ctx context.Context, address string, processExit <-chan error, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		connection, err := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(ctx, "tcp", address)
		if err == nil {
			_ = connection.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("sing-box probe listener did not become ready")
		case <-processExit:
			return fmt.Errorf("sing-box probe process exited before readiness")
		case <-ticker.C:
		}
	}
}

func queryExternalIP(ctx context.Context, proxyAddress, externalURL string, timeout time.Duration) (string, error) {
	transport := &http.Transport{
		DialContext: func(dialContext context.Context, network, address string) (net.Conn, error) {
			return socks5DialContext(dialContext, proxyAddress, network, address, timeout)
		},
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		IdleConnTimeout:       timeout,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: timeout}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, externalURL, nil)
	if err != nil {
		return "", fmt.Errorf("create external IP request")
	}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("request external IP")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("external IP service returned non-success status")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 65))
	if err != nil || len(body) > 64 {
		return "", fmt.Errorf("read bounded external IP response")
	}
	text := strings.TrimSpace(string(body))
	address, err := netip.ParseAddr(text)
	if err != nil || address.String() != text {
		return "", fmt.Errorf("external IP response is invalid")
	}
	return text, nil
}

func socks5DialContext(ctx context.Context, proxyAddress, network, targetAddress string, timeout time.Duration) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("SOCKS5 probe supports TCP only")
	}
	connection, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", proxyAddress)
	if err != nil {
		return nil, fmt.Errorf("connect to local SOCKS5 probe")
	}
	failed := true
	defer func() {
		if failed {
			_ = connection.Close()
		}
	}()
	deadline := time.Now().Add(timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = connection.SetDeadline(deadline)
	if _, err := connection.Write([]byte{5, 1, 0}); err != nil {
		return nil, fmt.Errorf("write SOCKS5 greeting")
	}
	greeting := make([]byte, 2)
	if _, err := io.ReadFull(connection, greeting); err != nil || greeting[0] != 5 || greeting[1] != 0 {
		return nil, fmt.Errorf("SOCKS5 greeting rejected")
	}
	host, portText, err := net.SplitHostPort(targetAddress)
	if err != nil {
		return nil, fmt.Errorf("SOCKS5 target is invalid")
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return nil, fmt.Errorf("SOCKS5 target port is invalid")
	}
	request := []byte{5, 1, 0}
	if address, parseErr := netip.ParseAddr(host); parseErr == nil && address.Is4() {
		request = append(request, 1)
		request = append(request, address.AsSlice()...)
	} else if parseErr == nil && address.Is6() {
		request = append(request, 4)
		request = append(request, address.AsSlice()...)
	} else {
		if len(host) == 0 || len(host) > 255 {
			return nil, fmt.Errorf("SOCKS5 target host is invalid")
		}
		request = append(request, 3, byte(len(host)))
		request = append(request, host...)
	}
	request = binary.BigEndian.AppendUint16(request, uint16(port))
	if _, err := connection.Write(request); err != nil {
		return nil, fmt.Errorf("write SOCKS5 connect request")
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(connection, header); err != nil || header[0] != 5 || header[1] != 0 {
		return nil, fmt.Errorf("SOCKS5 connect request rejected")
	}
	addressBytes := 0
	switch header[3] {
	case 1:
		addressBytes = 4
	case 4:
		addressBytes = 16
	case 3:
		length := make([]byte, 1)
		if _, err := io.ReadFull(connection, length); err != nil {
			return nil, fmt.Errorf("read SOCKS5 response address")
		}
		addressBytes = int(length[0])
	default:
		return nil, fmt.Errorf("SOCKS5 response address type is invalid")
	}
	if _, err := io.CopyN(io.Discard, connection, int64(addressBytes+2)); err != nil {
		return nil, fmt.Errorf("read SOCKS5 response")
	}
	_ = connection.SetDeadline(time.Time{})
	failed = false
	return connection, nil
}

func (probe LocalProxyProbe) timeout() time.Duration {
	if probe.Timeout <= 0 || probe.Timeout > time.Minute {
		return 10 * time.Second
	}
	return probe.Timeout
}
