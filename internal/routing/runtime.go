package routing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"

	"github.com/egress-manager/egress-manager/internal/domain"
)

// VerifyPolicyRuntime compares the complete selected priority and table, not
// merely the presence of one owned protocol marker. Unknown modifiers fail
// closed: they can alter packet selection or forwarding semantics.
func VerifyPolicyRuntime(intent RouteIntent, ipv4 bool, rulesJSON, routesJSON []byte) error {
	if err := intent.Validate(); err != nil {
		return err
	}
	rules, err := runtimeObjects(rulesJSON)
	if err != nil || len(rules) != 1 {
		return fmt.Errorf("policy rule inventory does not match desired state")
	}
	rule := rules[0]
	if !onlyFields(rule, "priority", "src", "srclen", "dst", "iif", "table", "protocol", "action", "flags") ||
		scalar(rule["priority"]) != strconv.FormatUint(uint64(intent.RulePriority), 10) ||
		scalar(rule["table"]) != strconv.FormatUint(uint64(intent.RoutingTable), 10) ||
		scalar(rule["protocol"]) != OwnedRouteProtocol || !emptyFlags(rule["flags"]) {
		return fmt.Errorf("policy rule attributes do not match desired state")
	}
	if action := scalar(rule["action"]); action != "" && action != "to_tbl" {
		return fmt.Errorf("unexpected policy rule action")
	}
	if destination := scalar(rule["dst"]); destination != "" && destination != "all" {
		return fmt.Errorf("unexpected policy rule destination")
	}
	source, input := scalar(rule["src"]), scalar(rule["iif"])
	if length := scalar(rule["srclen"]); length != "" {
		if address, err := netip.ParseAddr(source); err == nil {
			source = address.String() + "/" + length
		} else {
			return fmt.Errorf("invalid split policy source prefix")
		}
	}
	if intent.Source.Kind == domain.RouteSourceSubnet && intent.Source.Subnet.Addr().Is4() == ipv4 {
		prefix, err := netip.ParsePrefix(source)
		if err != nil || prefix.Masked() != intent.Source.Subnet.Masked() || input != "" {
			return fmt.Errorf("policy source selector differs")
		}
	} else if (source != "" && source != "all") || input != intent.IngressInterface {
		return fmt.Errorf("policy interface selector differs")
	}

	policy := string(intent.IPv6Policy)
	if ipv4 {
		policy = string(intent.IPv4Policy)
	}
	type expectedRoute struct{ kind, device string }
	expected := map[string]expectedRoute{}
	switch policy {
	case string(domain.IPv4FollowOutbound):
		expected["default"] = expectedRoute{"unicast", intent.TunnelInterface}
		for _, address := range intent.BypassAddresses {
			if address.Is4() == ipv4 {
				expected[netip.PrefixFrom(address, address.BitLen()).String()] = expectedRoute{"throw", ""}
			}
		}
	case string(domain.IPv4Block):
		expected["default"] = expectedRoute{"blackhole", ""}
	default:
		expected["default"] = expectedRoute{"throw", ""}
	}
	routes, err := runtimeObjects(routesJSON)
	if err != nil || len(routes) != len(expected) {
		return fmt.Errorf("route table inventory does not match desired state")
	}
	for _, route := range routes {
		if !onlyFields(route, "type", "dst", "dev", "table", "protocol", "scope", "metric", "flags", "pref") || scalar(route["protocol"]) != OwnedRouteProtocol || !emptyFlags(route["flags"]) {
			return fmt.Errorf("route attributes do not match desired state")
		}
		if table := scalar(route["table"]); table != "" && table != strconv.FormatUint(uint64(intent.RoutingTable), 10) {
			return fmt.Errorf("route table differs")
		}
		destination := scalar(route["dst"])
		if destination != "default" {
			if address, err := netip.ParseAddr(destination); err == nil {
				destination = netip.PrefixFrom(address, address.BitLen()).String()
			}
			if prefix, err := netip.ParsePrefix(destination); err == nil {
				destination = prefix.Masked().String()
			}
		}
		want, ok := expected[destination]
		kind := scalar(route["type"])
		if kind == "" {
			kind = "unicast"
		}
		device := scalar(route["dev"])
		if !ipv4 && want.kind != "unicast" && device == "lo" {
			device = ""
		}
		if !ok || kind != want.kind || device != want.device {
			return fmt.Errorf("route destination, type or interface differs")
		}
		metric := scalar(route["metric"])
		if metric != "" && metric != "0" && (ipv4 || metric != "1024") {
			return fmt.Errorf("unexpected route metric")
		}
		if preference := scalar(route["pref"]); preference != "" && preference != "medium" {
			return fmt.Errorf("unexpected route preference")
		}
		scope := scalar(route["scope"])
		if scope != "" && scope != "global" && !(want.kind == "unicast" && scope == "link") {
			return fmt.Errorf("unexpected route scope")
		}
		delete(expected, destination)
	}
	return nil
}

func runtimeObjects(content []byte) ([]map[string]json.RawMessage, error) {
	if len(content) > maximumNativeBytes {
		return nil, fmt.Errorf("runtime inventory exceeds limit")
	}
	var objects []map[string]json.RawMessage
	if err := json.Unmarshal(content, &objects); err != nil || objects == nil {
		return nil, fmt.Errorf("invalid runtime inventory")
	}
	for _, object := range objects {
		if object == nil {
			return nil, fmt.Errorf("null runtime object")
		}
	}
	return objects, nil
}

func scalar(value json.RawMessage) string {
	var text string
	if json.Unmarshal(value, &text) == nil {
		return text
	}
	var number uint32
	if json.Unmarshal(value, &number) == nil {
		return strconv.FormatUint(uint64(number), 10)
	}
	return ""
}

func onlyFields(object map[string]json.RawMessage, allowed ...string) bool {
	for key, value := range object {
		if key != "flags" {
			var text string
			var number uint32
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || (json.Unmarshal(value, &text) != nil && json.Unmarshal(value, &number) != nil) {
				return false
			}
		}
		found := false
		for _, name := range allowed {
			if key == name {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func emptyFlags(value json.RawMessage) bool {
	return len(value) == 0 || bytes.Equal(bytes.TrimSpace(value), []byte("[]"))
}
