package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/inventory"
	"github.com/egress-manager/egress-manager/internal/routeengine"
	"github.com/egress-manager/egress-manager/internal/routing"
	"github.com/egress-manager/egress-manager/internal/secrets"
	"github.com/egress-manager/egress-manager/internal/singbox"
	"github.com/egress-manager/egress-manager/internal/system"
)

type memoryJournal struct {
	operation domain.Transaction
	created   int
}

func (journal *memoryJournal) CreateOperation(_ context.Context, operation domain.Transaction) error {
	journal.operation = operation
	journal.created++
	return nil
}

func (journal *memoryJournal) TransitionOperation(_ context.Context, _ domain.ID, from, to domain.TransactionState, at time.Time, detail string) error {
	if journal.operation.State != from || !from.CanTransitionTo(to) {
		return fmt.Errorf("invalid journal transition")
	}
	journal.operation.State = to
	journal.operation.UpdatedAt = at
	journal.operation.FailureDetail = detail
	return nil
}

func (journal *memoryJournal) UnfinishedOperations(context.Context, int) ([]domain.Transaction, error) {
	return nil, nil
}

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--verify-runtime" {
		verifyRuntime(os.Args[2])
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "--bypass" {
		bypassRuntime(os.Args[2])
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "--reconcile" {
		reconcileRuntime(os.Args[2])
		return
	}
	if len(os.Args) != 2 {
		fail("usage: egress-routing-probe OUTPUT_DIRECTORY")
	}
	if err := os.MkdirAll(os.Args[1], 0o700); err != nil {
		fail("create output directory")
	}
	imports, err := singbox.ParseImport("socks5://10.203.2.3:1080#Lab%20outbound")
	if err != nil || len(imports) != 1 {
		fail("create lab outbound")
	}
	outbound := imports[0].Outbound
	credentials := map[domain.ID][]byte{outbound.ID: imports[0].CredentialDocument}
	interfaceName := ""
	if os.Getenv("EGRESS_ROUTE_ADAPTER") == string(domain.OutboundAdapterInterface) {
		credentials = nil
		interfaceName = os.Getenv("EGRESS_ROUTE_INTERFACE")
		outbound.ID = domain.ID(os.Getenv("EGRESS_ROUTE_OUTBOUND_ID"))
		outbound.Adapter = domain.OutboundAdapterInterface
		outbound.Server = domain.Endpoint{Host: "10.203.2.2", Port: 51820}
		outbound.Type = domain.OutboundWireGuard
		outbound.SecretMetadata = []string{"private_key"}
		if os.Getenv("EGRESS_ROUTE_TYPE") == string(domain.OutboundOpenVPN) {
			outbound.Type = domain.OutboundOpenVPN
			outbound.Server.Port = 1194
			outbound.SecretMetadata = []string{"profile"}
		}
		if interfaceName == "" || outbound.ID == "" {
			fail("native route interface and outbound ID are required")
		}
	}
	route := domain.Route{
		ID: "lab_route", Name: "Lab route", Source: domain.RouteSource{Kind: domain.RouteSourceSubnet, Subnet: netip.MustParsePrefix("10.203.1.0/24")},
		OutboundID: outbound.ID, FailurePolicy: domain.FailureBlock, DNSPolicy: domain.DNSFollowOutbound,
		DNSServers: []netip.Addr{netip.MustParseAddr("10.203.2.2")}, IPv4Policy: domain.IPv4FollowOutbound,
		IPv6Policy: domain.IPv6Block, KillSwitch: true, MTU: 1400, TCPMSS: 1360, Enabled: true,
	}
	host := labInventory()
	settings := routing.Settings{TableBase: 20000, RulePriorityBase: 21000, ProtectedLocalPrefixes: []netip.Prefix{netip.MustParsePrefix("10.203.2.0/24")}}
	if interfaceName != "" {
		host.Interfaces = append(host.Interfaces, inventory.Interface{Name: interfaceName, State: "up", MTU: 1400})
		settings.InterfaceOutbounds = map[domain.ID]string{outbound.ID: interfaceName}
		route.DNSPolicy = domain.DNSBlock
		route.DNSServers = nil
	}
	state, _ := routing.ParseState(nil, false)
	desired, err := routing.BuildPlan(settings, []domain.Route{route}, []domain.Outbound{outbound}, host, nil, state)
	if err != nil {
		fail("build routing desired state")
	}
	native, err := routing.BuildNativePlan(desired, false)
	if err != nil {
		fail("build native routing plan")
	}
	parsed, err := routing.ParseState(desired.Candidate(), true)
	if err != nil || len(parsed.Routes) != 1 {
		fail("parse generated routing state")
	}
	intent := parsed.Routes[0]
	singBoxState, _ := singbox.ParseState(nil, false)
	singBoxPlan, err := singbox.BuildRoutedPlan([]domain.Outbound{outbound}, credentials, parsed.Routes, singBoxState)
	if err != nil {
		fail("build routed sing-box candidate: " + err.Error())
	}
	files := map[string][]byte{
		"routing-state.json": desired.Candidate(),
		"sing-box.json":      singBoxPlan.Candidate(),
		"routing.nft":        native.NFTCandidate(), "routing-v4.batch": native.IPv4Batch(), "routing-v6.batch": native.IPv6Batch(),
		"tun-name": []byte(intent.TunnelInterface + "\n"), "table": []byte(fmt.Sprintf("%d\n", intent.RoutingTable)), "priority": []byte(fmt.Sprintf("%d\n", intent.RulePriority)),
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(os.Args[1], name), content, 0o600); err != nil {
			fail("write routing candidate")
		}
	}
}

func reconcileRuntime(directory string) {
	content, err := os.ReadFile(filepath.Join(directory, "routing-state.json"))
	if err != nil {
		fail("read routing state before reconciliation")
	}
	state, err := routing.ParseState(content, true)
	if err != nil || len(state.Routes) != 1 {
		fail("parse routing state before reconciliation")
	}
	protector, err := secrets.NewProtector([32]byte{2}, rand.Reader)
	if err != nil {
		fail("create reconciliation protector")
	}
	journal := &memoryJournal{}
	executor := routeengine.Executor{
		Runner: reconcileRunner{delegate: system.ExecRunner{}, tunnel: state.Routes[0].TunnelInterface}, Journal: journal, Protector: protector,
		SingBoxConfigPath: filepath.Join(directory, "sing-box.json"), RoutingStatePath: filepath.Join(directory, "routing-state.json"),
		BypassStatePath: filepath.Join(directory, "bypass.json"), InterfaceStatePath: filepath.Join(directory, "interfaces", "state.json"), InterfaceRuntimeDirectory: filepath.Join(directory, "interfaces"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := executor.ReconcileApplied(ctx, "network_lab_reconcile", labInventory(), nil); err != nil {
		fail("reconcile lost runtime: " + err.Error())
	}
	if err := executor.ReconcileApplied(ctx, "network_lab_reconcile_repeat", labInventory(), nil); err != nil || journal.created != 1 {
		fail("repeat reconciliation idempotently")
	}
}

type reconcileRunner struct {
	delegate system.Runner
	tunnel   string
}

func (runner reconcileRunner) Run(ctx context.Context, command system.Command) (system.Result, error) {
	joined := strings.Join(command.Args, " ")
	if command.Name == "systemctl" && joined == "restart egress-manager-sing-box.service" {
		result, err := runner.delegate.Run(ctx, system.Command{Name: "ip", Args: []string{"link", "add", runner.tunnel, "type", "dummy"}})
		if err != nil || result.ExitCode != 0 {
			return result, err
		}
		return runner.delegate.Run(ctx, system.Command{Name: "ip", Args: []string{"link", "set", runner.tunnel, "up"}})
	}
	if command.Name == "systemctl" && joined == "is-active --quiet egress-manager-sing-box.service" {
		return system.Result{ExitCode: 0}, nil
	}
	return runner.delegate.Run(ctx, command)
}

func labInventory() inventory.Inventory {
	return inventory.Inventory{Interfaces: []inventory.Interface{
		{Name: "lo", State: "unknown", MTU: 65536, Addresses: []inventory.Address{{Family: "inet", CIDR: "127.0.0.1/8", Scope: "host"}}},
		{Name: "r0", State: "up", MTU: 1500, Addresses: []inventory.Address{{Family: "inet", CIDR: "10.203.1.1/24", Scope: "global"}, {Family: "inet6", CIDR: "2001:db8:203:1::1/64", Scope: "global"}}},
		{Name: "r1", State: "up", MTU: 1500, Addresses: []inventory.Address{{Family: "inet", CIDR: "10.203.2.1/24", Scope: "global"}, {Family: "inet6", CIDR: "2001:db8:203:2::1/64", Scope: "global"}}},
	}}
}

func bypassRuntime(directory string) {
	statePath := filepath.Join(directory, "routing-state.json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		fail("read routing state before bypass")
	}
	protector, err := secrets.NewProtector([32]byte{1}, rand.Reader)
	if err != nil {
		fail("create bypass snapshot protector")
	}
	journal := &memoryJournal{}
	executor := routeengine.Executor{
		Runner: system.ExecRunner{}, Journal: journal, Protector: protector,
		SingBoxConfigPath: filepath.Join(directory, "sing-box.json"), RoutingStatePath: statePath,
		BypassStatePath: filepath.Join(directory, "bypass.json"), InterfaceStatePath: filepath.Join(directory, "interfaces", "state.json"),
		InterfaceRuntimeDirectory: filepath.Join(directory, "interfaces"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	response, err := executor.Bypass(ctx, "network_lab_bypass")
	if err != nil || response.State != domain.TransactionCommitted || response.AlreadyActive {
		fail("apply emergency bypass")
	}
	repeated, err := executor.Bypass(ctx, "network_lab_bypass_repeat")
	if err != nil || repeated.State != domain.TransactionCommitted || !repeated.AlreadyActive || journal.created != 1 {
		fail("repeat emergency bypass idempotently")
	}
	after, err := os.ReadFile(statePath)
	if err != nil || string(after) != string(before) {
		fail("bypass preserved routing state")
	}
}

func verifyRuntime(directory string) {
	content, err := os.ReadFile(filepath.Join(directory, "routing-state.json"))
	if err != nil {
		fail("read routing state")
	}
	state, err := routing.ParseState(content, true)
	if err != nil {
		fail("parse routing state")
	}
	runner := system.ExecRunner{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	nft, err := runner.Run(ctx, system.Command{Name: "nft", Args: []string{"-j", "list", "table", routing.OwnedNFTFamily, routing.OwnedNFTTable}})
	if err != nil || nft.ExitCode != 0 {
		fail("inspect runtime nftables")
	}
	if err := routing.VerifyNFTRuntime(state.Routes, nft.Stdout); err != nil {
		fail(err.Error())
	}
	for _, intent := range state.Routes {
		for _, family := range []string{"-4", "-6"} {
			rules, err := runner.Run(ctx, system.Command{Name: "ip", Args: []string{"-j", family, "rule", "show", "priority", strconv.FormatUint(uint64(intent.RulePriority), 10)}})
			if err != nil || rules.ExitCode != 0 {
				fail("inspect runtime rules")
			}
			routes, err := runner.Run(ctx, system.Command{Name: "ip", Args: []string{"-j", family, "route", "show", "table", strconv.FormatUint(uint64(intent.RoutingTable), 10)}})
			if err != nil || routes.ExitCode != 0 {
				fail("inspect runtime routes")
			}
			if err := routing.VerifyPolicyRuntime(intent, family == "-4", rules.Stdout, routes.Stdout); err != nil {
				fail(err.Error())
			}
		}
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "FAIL:", message)
	os.Exit(1)
}
