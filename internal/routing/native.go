package routing

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/system"
)

func InspectOwnedTable(ctx context.Context, runner system.Runner, timeout time.Duration) (bool, error) {
	if runner == nil {
		return false, fmt.Errorf("routing inspection runner is required")
	}
	if timeout <= 0 || timeout > 10*time.Second {
		timeout = 2 * time.Second
	}
	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := runner.Run(commandContext, system.Command{Name: "nft", Args: []string{"list", "tables"}})
	if err != nil || result.ExitCode != 0 {
		return false, errors.Join(err, fmt.Errorf("nft exited with code %d", result.ExitCode))
	}
	for _, line := range strings.Split(string(result.Stdout), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "table" && fields[1] == OwnedNFTFamily && fields[2] == OwnedNFTTable {
			return true, nil
		}
	}
	return false, nil
}

const (
	OwnedNFTFamily     = "inet"
	OwnedNFTTable      = "egm_egress"
	OwnedRouteProtocol = "242"
	maximumNativeBytes = 512 << 10
)

type NativePlan struct {
	Engine        string   `json:"engine"`
	StateHash     string   `json:"state_hash"`
	CandidateHash string   `json:"candidate_hash"`
	TableExisted  bool     `json:"table_existed"`
	EnabledRoutes int      `json:"enabled_routes"`
	Actions       []Action `json:"actions"`
	nftCandidate  []byte
	ipv4Batch     []byte
	ipv6Batch     []byte
}

func (plan NativePlan) NFTCandidate() []byte { return append([]byte{}, plan.nftCandidate...) }
func (plan NativePlan) IPv4Batch() []byte    { return append([]byte{}, plan.ipv4Batch...) }
func (plan NativePlan) IPv6Batch() []byte    { return append([]byte{}, plan.ipv6Batch...) }

func BuildNativePlan(desired ExecutionPlan, tableExisted bool) (NativePlan, error) {
	if desired.Plan.Engine != "policy-routing" || desired.Plan.StateHash == "" || desired.Plan.CandidateHash == "" || hash(desired.candidate) != desired.Plan.CandidateHash {
		return NativePlan{}, fmt.Errorf("routing execution plan is invalid")
	}
	state, err := ParseState(desired.candidate, true)
	if err != nil {
		return NativePlan{}, fmt.Errorf("routing candidate is not project-owned: %w", err)
	}
	intents := append([]RouteIntent{}, state.Routes...)
	sort.Slice(intents, func(i, j int) bool { return intents[i].ID < intents[j].ID })
	nft := renderNFT(intents, tableExisted)
	ipv4 := renderIPBatch(intents, true)
	ipv6 := renderIPBatch(intents, false)
	if len(nft)+len(ipv4)+len(ipv6) > maximumNativeBytes {
		return NativePlan{}, fmt.Errorf("native routing candidates exceed %d bytes", maximumNativeBytes)
	}
	digestInput := append(append(append([]byte{}, desired.candidate...), nft...), ipv4...)
	digestInput = append(digestInput, ipv6...)
	plan := NativePlan{
		Engine: "nftables+iproute2", StateHash: desired.Plan.StateHash, CandidateHash: hash(digestInput),
		TableExisted: tableExisted, EnabledRoutes: len(intents), Actions: []Action{},
		nftCandidate: nft, ipv4Batch: ipv4, ipv6Batch: ipv6,
	}
	operation := "create"
	if tableExisted {
		operation = "replace"
	}
	plan.Actions = append(plan.Actions, Action{Kind: operation, Resource: OwnedNFTFamily + " " + OwnedNFTTable, Summary: "Install only the project-owned route leak-control table."})
	for _, intent := range intents {
		plan.Actions = append(plan.Actions, Action{Kind: "policy_route", Resource: string(intent.ID), Summary: "Install owned IP-family routes and source policy rules."})
	}
	return plan, nil
}

func renderNFT(intents []RouteIntent, tableExisted bool) []byte {
	var builder strings.Builder
	if tableExisted {
		builder.WriteString("delete table inet egm_egress\n")
	}
	builder.WriteString("table inet egm_egress {\n")
	builder.WriteString("  chain input {\n    type filter hook input priority filter - 5; policy accept;\n")
	for _, intent := range intents {
		if intent.DNSPolicy != domain.DNSFollowOutbound && intent.DNSPolicy != domain.DNSBlock {
			continue
		}
		for _, ipv4 := range []bool{true, false} {
			fmt.Fprintf(&builder, "    %s meta l4proto { tcp, udp } th dport { 53, 853 } counter drop comment \"egm_%s_dns_input\"\n", nftSelector(intent, ipv4), intent.ID)
		}
	}
	builder.WriteString("  }\n")
	builder.WriteString("  chain forward {\n    type filter hook forward priority filter - 5; policy accept;\n")
	for _, intent := range intents {
		for _, ipv4 := range []bool{true, false} {
			selector := nftSelector(intent, ipv4)
			family := "ipv6"
			policy := string(intent.IPv6Policy)
			if ipv4 {
				family = "ipv4"
				policy = string(intent.IPv4Policy)
			}
			if intent.DNSPolicy == domain.DNSBlock {
				fmt.Fprintf(&builder, "    %s meta nfproto %s meta l4proto { tcp, udp } th dport { 53, 853 } counter drop comment \"egm_%s_dns_block\"\n", selector, family, intent.ID)
			} else if intent.DNSPolicy == domain.DNSSystem {
				fmt.Fprintf(&builder, "    %s meta nfproto %s meta l4proto { tcp, udp } th dport { 53, 853 } counter accept comment \"egm_%s_dns_system\"\n", selector, family, intent.ID)
			}
			if policy == string(domain.IPv4Block) {
				fmt.Fprintf(&builder, "    %s meta nfproto %s counter drop comment \"egm_%s_%s_block\"\n", selector, family, intent.ID, family)
				continue
			}
			if policy != string(domain.IPv4FollowOutbound) || !intent.KillSwitch {
				continue
			}
			if intent.TCPMSS != 0 {
				fmt.Fprintf(&builder, "    %s meta nfproto %s oifname \"%s\" meta l4proto tcp tcp flags syn tcp option maxseg size set %d comment \"egm_%s_mss\"\n", selector, family, intent.TunnelInterface, intent.TCPMSS, intent.ID)
			}
			fmt.Fprintf(&builder, "    %s meta nfproto %s oifname \"%s\" counter accept comment \"egm_%s_%s_tun\"\n", selector, family, intent.TunnelInterface, intent.ID, family)
			fmt.Fprintf(&builder, "    %s meta nfproto %s counter drop comment \"egm_%s_%s_kill\"\n", selector, family, intent.ID, family)
		}
	}
	builder.WriteString("  }\n}\n")
	return []byte(builder.String())
}

func nftSelector(intent RouteIntent, ipv4 bool) string {
	selector := fmt.Sprintf("iifname \"%s\"", intent.IngressInterface)
	if intent.Source.Kind != domain.RouteSourceSubnet || intent.Source.Subnet.Addr().Is4() != ipv4 {
		return selector
	}
	family := "ip6"
	if ipv4 {
		family = "ip"
	}
	return fmt.Sprintf("%s %s saddr %s", selector, family, intent.Source.Subnet)
}

func renderIPBatch(intents []RouteIntent, ipv4 bool) []byte {
	var builder strings.Builder
	for _, intent := range intents {
		policy := string(intent.IPv6Policy)
		if ipv4 {
			policy = string(intent.IPv4Policy)
		}
		if policy == string(domain.IPv4FollowOutbound) {
			for _, address := range intent.BypassAddresses {
				if address.Is4() != ipv4 {
					continue
				}
				bits := 128
				if ipv4 {
					bits = 32
				}
				fmt.Fprintf(&builder, "route replace unreachable %s table %d proto %s\n", netip.PrefixFrom(address, bits), intent.RoutingTable, OwnedRouteProtocol)
			}
			fmt.Fprintf(&builder, "route replace default dev %s table %d proto %s\n", intent.TunnelInterface, intent.RoutingTable, OwnedRouteProtocol)
		} else if policy == string(domain.IPv4Block) {
			fmt.Fprintf(&builder, "route replace blackhole default table %d proto %s\n", intent.RoutingTable, OwnedRouteProtocol)
		} else {
			fmt.Fprintf(&builder, "route replace throw default table %d proto %s\n", intent.RoutingTable, OwnedRouteProtocol)
		}
		selector := "iif " + intent.IngressInterface
		if intent.Source.Kind == domain.RouteSourceSubnet && intent.Source.Subnet.Addr().Is4() == ipv4 {
			selector = "from " + intent.Source.Subnet.String()
		}
		fmt.Fprintf(&builder, "rule add priority %d %s table %d protocol %s\n", intent.RulePriority, selector, intent.RoutingTable, OwnedRouteProtocol)
	}
	return []byte(builder.String())
}

// BuildIPBatches reconstructs bounded project-owned policy state for rollback.
func BuildIPBatches(intents []RouteIntent) ([]byte, []byte, error) {
	if len(intents) > MaximumRoutes {
		return nil, nil, fmt.Errorf("owned routing state exceeds %d routes", MaximumRoutes)
	}
	normalized := append([]RouteIntent{}, intents...)
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].ID < normalized[j].ID })
	seen := make(map[domain.ID]struct{}, len(normalized))
	for _, intent := range normalized {
		if err := intent.Validate(); err != nil {
			return nil, nil, err
		}
		if _, exists := seen[intent.ID]; exists {
			return nil, nil, fmt.Errorf("owned routing state contains duplicate routes")
		}
		seen[intent.ID] = struct{}{}
	}
	ipv4 := renderIPBatch(normalized, true)
	ipv6 := renderIPBatch(normalized, false)
	if len(ipv4)+len(ipv6) > maximumNativeBytes {
		return nil, nil, fmt.Errorf("owned IP batches exceed %d bytes", maximumNativeBytes)
	}
	return ipv4, ipv6, nil
}

func ParseNativeProtocol(value string) (uint8, error) {
	parsed, err := strconv.ParseUint(value, 10, 8)
	if err != nil || parsed == 0 {
		return 0, fmt.Errorf("owned route protocol is invalid")
	}
	return uint8(parsed), nil
}
