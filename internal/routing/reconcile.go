package routing

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/inventory"
)

func requireRoutingInventory(host inventory.Inventory) error {
	for _, warning := range host.Warnings {
		if strings.Contains(warning, "interface inventory") || strings.Contains(warning, "route inventory") || strings.Contains(warning, "policy rule inventory") {
			return fmt.Errorf("complete interface and dual-family routing inventory is required")
		}
	}
	return nil
}

// BuildReconciliationPlan replays applied state, never pending database edits.
// Slots are not reallocated: any foreign collision requires operator review.
func BuildReconciliationPlan(content []byte, host inventory.Inventory, protected []netip.Prefix) (ExecutionPlan, error) {
	state, err := ParseState(content, true)
	if err != nil {
		return ExecutionPlan{}, err
	}
	if err := requireRoutingInventory(host); err != nil {
		return ExecutionPlan{}, err
	}
	interfaces, err := indexInterfaces(host.Interfaces)
	if err != nil {
		return ExecutionPlan{}, err
	}
	for _, intent := range state.Routes {
		ingress, _, err := resolveSource(domain.Route{ID: intent.ID, Source: intent.Source}, interfaces, protected)
		if err != nil || ingress != intent.IngressInterface {
			return ExecutionPlan{}, fmt.Errorf("applied route source is no longer safe")
		}
		table := strconv.FormatUint(uint64(intent.RoutingTable), 10)
		for _, route := range host.Routes {
			if route.Table == table && route.Protocol != OwnedRouteProtocol {
				return ExecutionPlan{}, fmt.Errorf("foreign route occupies applied routing table")
			}
		}
		for _, rule := range host.PolicyRules {
			if (rule.Priority == int(intent.RulePriority) || rule.Table == table) && rule.Protocol != OwnedRouteProtocol {
				return ExecutionPlan{}, fmt.Errorf("foreign policy rule conflicts with applied route")
			}
		}
	}
	return ExecutionPlan{Plan: Plan{Engine: "policy-routing", StateHash: state.Hash, CandidateHash: hash(content), EnabledRoutes: len(state.Routes), Actions: []Action{{Kind: "reconcile", Resource: "owned egress routing state", Summary: "Restore previously applied routing without activating pending edits."}}}, candidate: append([]byte{}, content...)}, nil
}
