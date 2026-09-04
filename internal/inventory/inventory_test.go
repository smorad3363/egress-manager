package inventory

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/egress-manager/egress-manager/internal/system"
)

func TestParsersProduceTypedCanonicalInventory(t *testing.T) {
	t.Parallel()

	interfaces, err := parseInterfaces([]byte(`[
      {"ifname":"eth0","operstate":"UP","mtu":1500,"addr_info":[
        {"family":"inet","local":"192.0.2.9","prefixlen":24,"scope":"global"},
        {"family":"inet6","local":"2001:db8::9","prefixlen":64,"scope":"global"}
      ]},
      {"ifname":"lo","operstate":"UNKNOWN","mtu":65536,"addr_info":[{"family":"inet","local":"127.0.0.1","prefixlen":8,"scope":"host"}]}
    ]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(interfaces) != 2 || interfaces[0].Name != "eth0" || interfaces[0].Addresses[0].CIDR != "192.0.2.9/24" {
		t.Fatalf("interfaces = %#v", interfaces)
	}

	routes, err := parseRoutes([]byte(`[
      {"dst":"default","gateway":"192.0.2.1","dev":"eth0","protocol":"dhcp","table":"main","metric":100},
      {"dst":"10.0.0.0/8","dev":"wg0","protocol":"static","table":100}
    ]`))
	if err != nil {
		t.Fatal(err)
	}
	if !routes[0].Default || routes[0].Gateway != "192.0.2.1" || routes[1].Table != "100" {
		t.Fatalf("routes = %#v", routes)
	}

	listeners, err := parseListeners([]byte("tcp LISTEN 0 4096 127.0.0.1:443 0.0.0.0:* users:((\"haproxy\",pid=42,fd=7))\nudp UNCONN 0 0 [::]:53 [::]:*\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(listeners) != 2 || listeners[0].Process != "haproxy" || listeners[0].PID != 42 || listeners[1].Address != "::" {
		t.Fatalf("listeners = %#v", listeners)
	}

	dns := parseDNS([]byte("nameserver 1.1.1.1\nnameserver 2001:4860:4860::8888\nsearch Example.COM internal.example.\n"))
	if !reflect.DeepEqual(dns.Servers, []string{"1.1.1.1", "2001:4860:4860::8888"}) || !reflect.DeepEqual(dns.SearchDomains, []string{"example.com", "internal.example"}) {
		t.Fatalf("DNS = %#v", dns)
	}
}

func TestParsersRejectMalformedPrimaryDocuments(t *testing.T) {
	t.Parallel()

	if _, err := parseInterfaces([]byte(`{"ifname":"eth0"}`)); err == nil {
		t.Fatal("parseInterfaces() accepted a non-array document")
	}
	if _, err := parseRoutes([]byte(`not-json`)); err == nil {
		t.Fatal("parseRoutes() accepted malformed JSON")
	}
}

func TestDetectConflictsHandlesWildcardAndProtocol(t *testing.T) {
	t.Parallel()

	listeners := []Listener{
		{Protocol: "tcp", Address: "0.0.0.0", Port: 443, Process: "haproxy", PID: 42},
		{Protocol: "udp", Address: "192.0.2.9", Port: 53, Process: "resolved", PID: 9},
	}
	conflicts, err := DetectConflicts([]Binding{
		{Protocol: "TCP", Address: "127.0.0.1", Port: 443},
		{Protocol: "udp", Address: "192.0.2.10", Port: 53},
	}, listeners)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 || conflicts[0].Existing.Process != "haproxy" {
		t.Fatalf("conflicts = %#v", conflicts)
	}
	if _, err := DetectConflicts([]Binding{{Protocol: "sctp", Address: "127.0.0.1", Port: 443}}, listeners); err == nil {
		t.Fatal("DetectConflicts() accepted an unsupported protocol")
	}
}

type fakeRunner struct {
	mu      sync.Mutex
	results map[string]system.Result
	calls   []system.Command
}

func (runner *fakeRunner) Run(_ context.Context, command system.Command) (system.Result, error) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.calls = append(runner.calls, command)
	result, exists := runner.results[command.Name+" "+joinArgs(command.Args)]
	if !exists {
		return system.Result{ExitCode: -1}, errors.New("executable unavailable")
	}
	return result, nil
}

func joinArgs(values []string) string {
	result := ""
	for index, value := range values {
		if index > 0 {
			result += " "
		}
		result += value
	}
	return result
}

type fakeFiles map[string][]byte

func (files fakeFiles) ReadFile(path string) ([]byte, error) {
	value, exists := files[path]
	if !exists {
		return nil, errors.New("missing file")
	}
	return value, nil
}

func TestCollectorReturnsPartialStateAndDetectsBackends(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{results: map[string]system.Result{
		"ip -j address show":         {Stdout: []byte(`[{"ifname":"eth0","operstate":"UP","mtu":1500,"addr_info":[]}]`), ExitCode: 0},
		"ip -j route show table all": {Stdout: []byte(`malformed`), ExitCode: 0},
		"ss -H -lntu -p":             {Stdout: []byte("tcp LISTEN 0 4096 0.0.0.0:22 0.0.0.0:*\n"), ExitCode: 0},
		"ps -eo comm=,args=":         {Stdout: []byte("haproxy /usr/sbin/haproxy -f /etc/haproxy/haproxy.cfg\npython /opt/marzban/main.py\n"), ExitCode: 0},
		"nft --version":              {Stdout: []byte("nftables v1.0.9\n"), ExitCode: 0},
		"iptables --version":         {Stdout: []byte("iptables v1.8.9 (nf_tables)\n"), ExitCode: 0},
		"haproxy -vv":                {Stdout: []byte("HAProxy version 2.8.5\n"), ExitCode: 0},
	}}
	collector := Collector{
		Runner: runner,
		Files:  fakeFiles{"/etc/resolv.conf": []byte("nameserver 1.1.1.1\n")},
		Now:    func() time.Time { return time.Unix(1_700_000_000, 0) },
	}
	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Interfaces) != 1 || len(result.Routes) != 0 || len(result.Listeners) != 1 {
		t.Fatalf("inventory = %#v", result)
	}
	if !reflect.DeepEqual(result.Warnings, []string{"route inventory malformed"}) {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
	capabilities := make(map[string]Capability)
	for _, capability := range result.Capabilities {
		capabilities[capability.Name] = capability
	}
	if capabilities["iptables"].Backend != "nf_tables" || !capabilities["HAProxy"].Running || !capabilities["Marzban"].Running {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	for _, call := range runner.calls {
		if call.Name == "sh" || call.Name == "bash" || call.Name == "cmd" {
			t.Fatalf("collector invoked a shell: %#v", call)
		}
	}
}
