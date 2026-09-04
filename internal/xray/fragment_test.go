package xray

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildFragmentPlanPrependsOwnedRulesAndPreservesForeignRouting(t *testing.T) {
	t.Parallel()
	installation := managedInstallation()
	foreignRouting := []byte(`{"domainStrategy":"IPIfNonMatch","rules":[{"type":"field","ip":["geoip:private"],"outboundTag":"blocked"}]}`)
	fragment, err := ParseFragmentState(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildFragmentPlan(installation, []Binding{
		{ID: "route_b", InboundTag: "in-b", OutboundTag: "proxy", Enabled: true},
		{ID: "route_a", InboundTag: "in-a", OutboundTag: "direct", Enabled: true},
	}, ForeignRoutingState{Hash: "foreign-hash", Routing: foreignRouting}, fragment)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Review.Engine != "xray" || plan.Review.EnabledBindings != 2 || !plan.Review.CandidateExists || len(plan.Review.Actions) != 3 || strings.Contains(string(mustJSON(t, plan.Review)), "geoip:private") {
		t.Fatalf("review = %#v", plan.Review)
	}
	var document struct {
		Routing struct {
			DomainStrategy string `json:"domainStrategy"`
			Rules          []struct {
				RuleTag     string   `json:"ruleTag"`
				InboundTag  []string `json:"inboundTag"`
				OutboundTag string   `json:"outboundTag"`
				IP          []string `json:"ip"`
			} `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(plan.Candidate(), &document); err != nil {
		t.Fatal(err)
	}
	if document.Routing.DomainStrategy != "IPIfNonMatch" || len(document.Routing.Rules) != 3 || document.Routing.Rules[0].RuleTag != "egm-route-route_a" || document.Routing.Rules[1].RuleTag != "egm-route-route_b" || len(document.Routing.Rules[2].IP) != 1 {
		t.Fatalf("candidate = %s", plan.Candidate())
	}
	parsed, err := ParseFragmentState(plan.Candidate(), true)
	if err != nil || parsed.Hash != plan.Review.CandidateHash {
		t.Fatalf("parsed = %#v, err = %v", parsed, err)
	}
}

func TestBuildFragmentPlanRejectsReadOnlyMissingAndDuplicateBindings(t *testing.T) {
	t.Parallel()
	fragment, _ := ParseFragmentState(nil, false)
	foreign := ForeignRoutingState{Hash: "foreign-hash", Routing: []byte(`{"rules":[]}`)}
	readOnly := managedInstallation()
	readOnly.MutationStrategy = ReadOnly
	if _, err := BuildFragmentPlan(readOnly, nil, foreign, fragment); err == nil {
		t.Fatal("accepted a read-only installation")
	}
	for _, bindings := range [][]Binding{
		{{ID: "missing_in", InboundTag: "missing", OutboundTag: "direct", Enabled: true}},
		{{ID: "missing_out", InboundTag: "in-a", OutboundTag: "missing", Enabled: true}},
		{
			{ID: "first", InboundTag: "in-a", OutboundTag: "direct", Enabled: true},
			{ID: "second", InboundTag: "in-a", OutboundTag: "proxy", Enabled: true},
		},
	} {
		if _, err := BuildFragmentPlan(managedInstallation(), bindings, foreign, fragment); err == nil {
			t.Fatalf("accepted bindings %#v", bindings)
		}
	}
}

func TestBuildFragmentPlanDeletesOnlyOwnedFragmentWhenNoBindingsRemain(t *testing.T) {
	t.Parallel()
	existing := []byte("{\"routing\":{\"rules\":[{\"type\":\"field\",\"ruleTag\":\"egm-route-old\",\"inboundTag\":[\"in-a\"],\"outboundTag\":\"direct\"}]}}\n")
	fragment, err := ParseFragmentState(existing, true)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildFragmentPlan(managedInstallation(), nil, ForeignRoutingState{Hash: "foreign-hash", Routing: []byte(`{"rules":[]}`)}, fragment)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Review.CandidateExists || len(plan.Candidate()) != 0 || len(plan.Review.Actions) != 1 || plan.Review.Actions[0].Kind != "delete" {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestFragmentParserRejectsForeignShapeAndReservedCollision(t *testing.T) {
	t.Parallel()
	for _, content := range []string{
		`{"routing":{"rules":[]}}`,
		`{"routing":{"rules":[{"ruleTag":"foreign"},{"ruleTag":"egm-route-late"}]}}`,
		`{"routing":{"rules":[{"ruleTag":"egm-route-one"}]},"outbounds":[]}`,
	} {
		if _, err := ParseFragmentState([]byte(content), true); err == nil {
			t.Fatalf("accepted fragment %s", content)
		}
	}
	if _, _, err := decodeRouting([]byte(`{"rules":[{"ruleTag":"egm-route-collision"}]}`), false); err == nil {
		t.Fatal("accepted reserved ruleTag in foreign routing")
	}
}

func managedInstallation() Installation {
	return Installation{
		Kind: Standalone, ConfigRoot: "/etc/xray", ServiceName: "xray.service", Loader: "confdir",
		ManagedPath: "/etc/xray/" + managedFragmentName, Ownership: ForeignOwnership, MutationStrategy: ManagedFragment,
		InboundTags: []string{"in-a", "in-b"}, OutboundTags: []string{"direct", "proxy"},
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return content
}
