package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	managed "github.com/egress-manager/egress-manager/internal/singbox"
	"github.com/egress-manager/egress-manager/internal/system"
)

func main() {
	if len(os.Args) != 2 {
		fail("usage: egress-singbox-probe FIXTURE_DIRECTORY")
	}
	entries, err := os.ReadDir(os.Args[1])
	if err != nil {
		fail("read fixture directory")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	validated := 0
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "" {
			continue
		}
		input, err := os.ReadFile(filepath.Join(os.Args[1], entry.Name()))
		if err != nil {
			fail("read import fixture")
		}
		imports, err := managed.ParseImport(strings.TrimSpace(string(input)))
		if err != nil {
			fail("parse import fixture " + entry.Name())
		}
		for _, imported := range imports {
			state, _ := managed.ParseState(nil, false)
			plan, err := managed.BuildPlan([]domain.Outbound{imported.Outbound}, map[domain.ID][]byte{imported.Outbound.ID: imported.CredentialDocument}, state)
			if err != nil {
				fail("plan import fixture " + entry.Name())
			}
			path := filepath.Join(os.TempDir(), "egress-sing-box-check.json")
			if err := os.WriteFile(path, plan.Candidate(), 0o600); err != nil {
				fail("write candidate")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			result, runErr := (system.ExecRunner{}).Run(ctx, system.Command{Name: "sing-box", Args: []string{"check", "-c", path}})
			cancel()
			_ = os.Remove(path)
			if runErr != nil || result.ExitCode != 0 {
				fail(fmt.Sprintf("native validation failed for %s (%s)", entry.Name(), imported.Outbound.Type))
			}
			validated++
		}
	}
	if validated != 10 {
		fail(fmt.Sprintf("validated %d candidates, want 10", validated))
	}
	fmt.Println("PASS: sing-box fixture imports pass native validation")
	probeConnectivity()
}

func probeConnectivity() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	httpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fail("listen for sing-box external IP fixture")
	}
	httpServer := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "203.0.113.88")
	})}
	go func() { _ = httpServer.Serve(httpListener) }()
	defer func() { _ = httpServer.Shutdown(context.Background()) }()

	socksListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fail("listen for SOCKS5 fixture")
	}
	defer socksListener.Close()
	go serveSOCKS(ctx, socksListener)

	input := fmt.Sprintf("socks5://%s#Integration%%20SOCKS5", socksListener.Addr().String())
	imports, err := managed.ParseImport(input)
	if err != nil || len(imports) != 1 {
		fail("parse integration SOCKS5 outbound")
	}
	imported := imports[0]
	tester := managed.Tester{
		Validator: managed.NativeValidator{Runner: system.ExecRunner{}, TempDirectory: os.TempDir(), Timeout: 5 * time.Second},
		Dialer:    &net.Dialer{Timeout: 2 * time.Second},
		Probe:     managed.LocalProxyProbe{TempDirectory: os.TempDir(), ExternalIPURL: "http://" + httpListener.Addr().String(), Timeout: 8 * time.Second},
		Timeout:   12 * time.Second,
	}
	health := tester.Test(ctx, imported.Outbound, imported.CredentialDocument)
	if health.Status != domain.HealthHealthy || health.ConfigurationValid != domain.ProbePassed || health.TransportReachable != domain.ProbePassed || health.InternetReachable != domain.ProbePassed || health.TCP != domain.ProbePassed || health.UDP != domain.ProbeUntestable || health.ExternalIP != "203.0.113.88" {
		fail("sing-box isolated connectivity health result is incomplete")
	}
	fmt.Println("PASS: sing-box health distinguishes validation, transport, internet, external IP, TCP, and UDP testability")
}

func serveSOCKS(ctx context.Context, listener net.Listener) {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		go handleSOCKS(ctx, connection)
	}
}

func handleSOCKS(ctx context.Context, connection net.Conn) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReader(connection)
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(reader, methods); err != nil {
		return
	}
	if _, err := connection.Write([]byte{5, 0}); err != nil {
		return
	}
	request := make([]byte, 4)
	if _, err := io.ReadFull(reader, request); err != nil || request[0] != 5 || request[1] != 1 {
		return
	}
	var host string
	switch request[3] {
	case 1:
		address := make([]byte, 4)
		if _, err := io.ReadFull(reader, address); err != nil {
			return
		}
		host = net.IP(address).String()
	case 4:
		address := make([]byte, 16)
		if _, err := io.ReadFull(reader, address); err != nil {
			return
		}
		host = net.IP(address).String()
	case 3:
		length, err := reader.ReadByte()
		if err != nil {
			return
		}
		name := make([]byte, int(length))
		if _, err := io.ReadFull(reader, name); err != nil {
			return
		}
		host = string(name)
	default:
		return
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(reader, portBytes); err != nil {
		return
	}
	target := net.JoinHostPort(host, fmt.Sprint(binary.BigEndian.Uint16(portBytes)))
	upstream, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", target)
	if err != nil {
		_, _ = connection.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer upstream.Close()
	if _, err := connection.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
		return
	}
	_ = connection.SetDeadline(time.Time{})
	done := make(chan struct{}, 1)
	go func() { _, _ = io.Copy(upstream, reader); done <- struct{}{} }()
	_, _ = io.Copy(connection, upstream)
	<-done
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "FAIL:", message)
	os.Exit(1)
}
