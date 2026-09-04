package routing

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"github.com/egress-manager/egress-manager/internal/domain"
)

type nftObject = map[string]any

// VerifyNFTRuntime checks the complete owned table, including rule ordering and
// expressions. Only kernel handles and anonymous counter values are volatile.
func VerifyNFTRuntime(intents []RouteIntent, content []byte) error {
	expected, err := expectedNFTObjects(intents)
	if err != nil {
		return err
	}
	if len(content) > maximumNativeBytes {
		return fmt.Errorf("nftables runtime inventory exceeds limit")
	}
	var document struct {
		Objects []nftObject `json:"nftables"`
	}
	if err := json.Unmarshal(content, &document); err != nil || document.Objects == nil {
		return fmt.Errorf("invalid nftables runtime inventory")
	}
	actual := []nftObject{}
	for _, object := range document.Objects {
		if len(object) != 1 {
			return fmt.Errorf("invalid nftables runtime object")
		}
		if _, metadata := object["metainfo"]; metadata {
			continue
		}
		for kind, value := range object {
			if kind != "table" && kind != "chain" && kind != "rule" {
				return fmt.Errorf("unexpected nftables runtime object")
			}
			fields, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("invalid nftables runtime fields")
			}
			delete(fields, "handle")
			if kind == "rule" {
				expressions, ok := fields["expr"].([]any)
				if !ok {
					return fmt.Errorf("invalid nftables runtime expressions")
				}
				for _, expression := range expressions {
					item, ok := expression.(map[string]any)
					if !ok {
						return fmt.Errorf("invalid nftables runtime expression")
					}
					if counter, exists := item["counter"]; exists {
						values, ok := counter.(map[string]any)
						if !ok || len(values) != 2 {
							return fmt.Errorf("unexpected nftables counter")
						}
						if _, ok := values["packets"].(float64); !ok {
							return fmt.Errorf("invalid nftables counter")
						}
						if _, ok := values["bytes"].(float64); !ok {
							return fmt.Errorf("invalid nftables counter")
						}
						item["counter"] = nil
					}
				}
			}
		}
		actual = append(actual, object)
	}
	// Decode generated numbers using the same representation as native JSON.
	encoded, _ := json.Marshal(expected)
	var normalized []nftObject
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return err
	}
	if !reflect.DeepEqual(actual, normalized) {
		return fmt.Errorf("owned nftables expressions differ from desired state")
	}
	return nil
}

func expectedNFTObjects(intents []RouteIntent) ([]nftObject, error) {
	if len(intents) > MaximumRoutes {
		return nil, fmt.Errorf("too many runtime routes")
	}
	intents = append([]RouteIntent{}, intents...)
	sort.Slice(intents, func(i, j int) bool { return intents[i].ID < intents[j].ID })
	objects := []nftObject{{"table": nftObject{"family": OwnedNFTFamily, "name": OwnedNFTTable}}}
	for _, name := range []string{"input", "forward"} {
		objects = append(objects, nftObject{"chain": nftObject{"family": OwnedNFTFamily, "table": OwnedNFTTable, "name": name, "type": "filter", "hook": name, "prio": -5, "policy": "accept"}})
	}
	add := func(chain, comment string, expr []any) {
		objects = append(objects, nftObject{"rule": nftObject{"family": OwnedNFTFamily, "table": OwnedNFTTable, "chain": chain, "comment": comment, "expr": expr}})
	}
	for _, intent := range intents {
		if err := intent.Validate(); err != nil {
			return nil, err
		}
		if intent.DNSPolicy != domain.DNSFollowOutbound && intent.DNSPolicy != domain.DNSBlock {
			continue
		}
		for _, ipv4 := range []bool{true, false} {
			expr := nftSelectorObjects(intent, ipv4)
			expr = append(expr, nftDNSObjects()...)
			add("input", "egm_"+string(intent.ID)+"_dns_input", append(expr, nftObject{"counter": nil}, nftObject{"drop": nil}))
		}
	}
	for _, intent := range intents {
		for _, ipv4 := range []bool{true, false} {
			family, policy := "ipv6", string(intent.IPv6Policy)
			if ipv4 {
				family, policy = "ipv4", string(intent.IPv4Policy)
			}
			base := append(nftSelectorObjects(intent, ipv4), nftMatch("==", nftMeta("nfproto"), family))
			clone := func() []any { return append([]any{}, base...) }
			prefix := "egm_" + string(intent.ID)
			if intent.DNSPolicy == domain.DNSBlock || intent.DNSPolicy == domain.DNSSystem {
				expr := append(clone(), nftDNSObjects()...)
				verdict, suffix := "drop", "_dns_block"
				if intent.DNSPolicy == domain.DNSSystem {
					verdict, suffix = "accept", "_dns_system"
				}
				add("forward", prefix+suffix, append(expr, nftObject{"counter": nil}, nftObject{verdict: nil}))
			}
			if policy == string(domain.IPv4Block) {
				add("forward", prefix+"_"+family+"_block", append(clone(), nftObject{"counter": nil}, nftObject{"drop": nil}))
				continue
			}
			if policy != string(domain.IPv4FollowOutbound) || !intent.KillSwitch {
				continue
			}
			output := nftMatch("==", nftMeta("oifname"), intent.TunnelInterface)
			if intent.TCPMSS != 0 {
				expr := append(clone(), output, nftMatch("in", nftPayload("tcp", "flags"), "syn"), nftObject{"mangle": nftObject{"key": nftObject{"tcp option": nftObject{"name": "maxseg", "field": "size"}}, "value": intent.TCPMSS}})
				add("forward", prefix+"_mss", expr)
			}
			add("forward", prefix+"_"+family+"_tun", append(clone(), output, nftObject{"counter": nil}, nftObject{"accept": nil}))
			add("forward", prefix+"_"+family+"_kill", append(clone(), nftObject{"counter": nil}, nftObject{"drop": nil}))
		}
	}
	return objects, nil
}

func nftMeta(key string) nftObject { return nftObject{"meta": nftObject{"key": key}} }
func nftPayload(protocol, field string) nftObject {
	return nftObject{"payload": nftObject{"protocol": protocol, "field": field}}
}
func nftMatch(op string, left, right any) nftObject {
	return nftObject{"match": nftObject{"op": op, "left": left, "right": right}}
}
func nftSelectorObjects(intent RouteIntent, ipv4 bool) []any {
	expr := []any{nftMatch("==", nftMeta("iifname"), intent.IngressInterface)}
	if intent.Source.Kind == domain.RouteSourceSubnet && intent.Source.Subnet.Addr().Is4() == ipv4 {
		protocol := "ip6"
		if ipv4 {
			protocol = "ip"
		}
		expr = append(expr, nftMatch("==", nftPayload(protocol, "saddr"), nftObject{"prefix": nftObject{"addr": intent.Source.Subnet.Masked().Addr().String(), "len": intent.Source.Subnet.Bits()}}))
	}
	return expr
}
func nftDNSObjects() []any {
	return []any{nftMatch("==", nftMeta("l4proto"), nftObject{"set": []string{"tcp", "udp"}}), nftMatch("==", nftPayload("th", "dport"), nftObject{"set": []int{53, 853}})}
}
