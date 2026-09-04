package haproxy

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
)

type scriptedRuntimeDialer struct {
	mu        sync.Mutex
	responses [][]byte
	commands  []string
}

func (dialer *scriptedRuntimeDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	client, server := net.Pipe()
	dialer.mu.Lock()
	response := dialer.responses[0]
	dialer.responses = dialer.responses[1:]
	dialer.mu.Unlock()
	go func() {
		defer server.Close()
		scanner := bufio.NewScanner(server)
		var command string
		for scanner.Scan() {
			if scanner.Text() == "quit" {
				break
			}
			if command == "" {
				command = scanner.Text()
			}
		}
		dialer.mu.Lock()
		dialer.commands = append(dialer.commands, command)
		dialer.mu.Unlock()
		_, _ = server.Write(response)
	}()
	return client, nil
}

func TestRuntimeClientReturnsOnlyOwnedTypedStatistics(t *testing.T) {
	info := []byte("Name: HAProxy\nVersion: 3.0.5\nPid: 42\nUptime_sec: 120\nCurrConns: 3\nCumConns: 99\n")
	stats := []byte(`# pxname,svname,scur,smax,stot,bin,bout,econ,eresp,status,chkfail,downtime
egm_fe_public_api,FRONTEND,2,8,40,1000,2000,0,0,OPEN,,
egm_pool_public_api,egm_srv_api_primary,1,7,35,900,1800,2,1,UP,3,10
egm_pool_public_api,BACKEND,2,8,40,1000,2000,2,1,UP,3,10
foreign,FRONTEND,9,9,9,9,9,9,9,OPEN,9,9
`)
	dialer := &scriptedRuntimeDialer{responses: [][]byte{info, stats}}
	snapshot, err := (RuntimeClient{SocketPath: "/run/egress-manager/haproxy-runtime.sock", Dialer: dialer}).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Info.PID != 42 || snapshot.Info.TotalConnections != 99 || len(snapshot.Stats) != 3 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	server := snapshot.Stats[1]
	if server.Kind != "server" || server.BackendID != "api_primary" || server.Status != "healthy" || server.CheckFailures != 3 || server.BytesOut != 1800 {
		t.Fatalf("server stat = %#v", server)
	}
	if strings.Join(dialer.commands, ",") != "show info,show stat" {
		t.Fatalf("commands = %#v", dialer.commands)
	}
}

func TestRuntimeParsersRejectMissingAndMalformedFields(t *testing.T) {
	t.Parallel()
	if _, err := parseRuntimeInfo([]byte("Name: HAProxy\n")); err == nil {
		t.Fatal("parseRuntimeInfo() accepted missing numeric fields")
	}
	if _, err := parseRuntimeStats([]byte("# pxname,svname\n")); err == nil {
		t.Fatal("parseRuntimeStats() accepted missing fields")
	}
	malformed := []byte("# pxname,svname,scur,smax,stot,bin,bout,econ,eresp,status,chkfail,downtime\negm_fe_api,FRONTEND,bad,0,0,0,0,0,0,OPEN,0,0\n")
	if _, err := parseRuntimeStats(malformed); err == nil {
		t.Fatal("parseRuntimeStats() accepted malformed counter")
	}
}

func TestRuntimeClientRejectsCommandInjection(t *testing.T) {
	t.Parallel()
	client := RuntimeClient{SocketPath: "/run/egress-manager/haproxy-runtime.sock", Dialer: &scriptedRuntimeDialer{responses: [][]byte{{}}}}
	if _, err := client.query(context.Background(), "show info\nshutdown sessions all"); err == nil {
		t.Fatal("Runtime API accepted multiline command")
	}
}
