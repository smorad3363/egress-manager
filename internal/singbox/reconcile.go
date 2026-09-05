package singbox

import (
	"encoding/json"
	"fmt"
)

// BuildReconciliationPlan retains the exact applied private configuration.
func BuildReconciliationPlan(content []byte) (ExecutionPlan, error) {
	state, err := ParseState(content, true)
	if err != nil {
		return ExecutionPlan{}, err
	}
	var configuration generatedConfiguration
	if err := json.Unmarshal(content, &configuration); err != nil {
		return ExecutionPlan{}, fmt.Errorf("invalid applied sing-box configuration")
	}
	count := len(configuration.Outbounds) + len(configuration.Endpoints)
	for _, outbound := range configuration.Outbounds {
		if outbound["tag"] == directRouteTag {
			count--
		}
	}
	return ExecutionPlan{Plan: Plan{Engine: "sing-box", StateHash: state.Hash, CandidateHash: hashBytes(content), EnabledOutbounds: count, RoutedRoutes: len(configuration.Inbounds), Actions: []Action{{Kind: "reconcile", Resource: "owned sing-box configuration", Summary: "Restore previously applied private configuration."}}}, candidate: append([]byte{}, content...)}, nil
}
