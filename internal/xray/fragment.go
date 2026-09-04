package xray

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/egress-manager/egress-manager/internal/domain"
)

const (
	MaximumBindings = 256
	ownedRulePrefix = "egm-route-"
)

type Binding = domain.XrayBinding

// ForeignRoutingState describes the effective foreign routing object after all
// foreign confdir files have been merged, but before the owned fragment.
type ForeignRoutingState struct {
	Hash    string `json:"hash"`
	Routing []byte `json:"-"`
}

type FragmentState struct {
	Exists bool   `json:"exists"`
	Hash   string `json:"hash"`
}

type FragmentAction struct {
	Kind     string `json:"kind"`
	Resource string `json:"resource"`
	Summary  string `json:"summary"`
}

type FragmentReview struct {
	Engine            string           `json:"engine"`
	ServiceName       string           `json:"service_name"`
	ManagedPath       string           `json:"managed_path"`
	ForeignStateHash  string           `json:"foreign_state_hash"`
	FragmentStateHash string           `json:"fragment_state_hash"`
	CandidateHash     string           `json:"candidate_hash"`
	CandidateExists   bool             `json:"candidate_exists"`
	EnabledBindings   int              `json:"enabled_bindings"`
	Actions           []FragmentAction `json:"actions"`
}

type FragmentExecutionPlan struct {
	Review    FragmentReview
	candidate []byte
}

func (plan FragmentExecutionPlan) Candidate() []byte {
	return append([]byte{}, plan.candidate...)
}

func ParseFragmentState(content []byte, exists bool) (FragmentState, error) {
	if !exists {
		if len(content) != 0 {
			return FragmentState{}, fmt.Errorf("absent Xray fragment contains data")
		}
		return FragmentState{Hash: hashFragmentState(nil, false)}, nil
	}
	if len(content) == 0 || len(content) > MaximumConfigurationBytes {
		return FragmentState{}, fmt.Errorf("owned Xray fragment is empty or oversized")
	}
	var document struct {
		Routing json.RawMessage `json:"routing"`
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil || len(document.Routing) == 0 {
		return FragmentState{}, fmt.Errorf("owned Xray fragment is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return FragmentState{}, fmt.Errorf("owned Xray fragment has trailing data")
	}
	if _, _, err := decodeRouting(document.Routing, true); err != nil {
		return FragmentState{}, err
	}
	return FragmentState{Exists: true, Hash: hashFragmentState(content, true)}, nil
}

func BuildFragmentPlan(installation Installation, bindings []Binding, foreign ForeignRoutingState, fragment FragmentState) (FragmentExecutionPlan, error) {
	if err := validateManagedInstallation(installation); err != nil {
		return FragmentExecutionPlan{}, err
	}
	if foreign.Hash == "" || fragment.Hash == "" {
		return FragmentExecutionPlan{}, fmt.Errorf("Xray foreign and fragment state hashes are required")
	}
	if len(foreign.Routing) > MaximumConfigurationBytes {
		return FragmentExecutionPlan{}, fmt.Errorf("Xray foreign routing state is oversized")
	}
	if len(bindings) > MaximumBindings {
		return FragmentExecutionPlan{}, fmt.Errorf("Xray desired state exceeds %d bindings", MaximumBindings)
	}
	routing, foreignRules, err := decodeRouting(foreign.Routing, false)
	if err != nil {
		return FragmentExecutionPlan{}, fmt.Errorf("decode foreign Xray routing: %w", err)
	}
	normalized := append([]Binding{}, bindings...)
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].ID < normalized[j].ID })
	availableInbounds := stringSet(installation.InboundTags)
	availableOutbounds := stringSet(installation.OutboundTags)
	seenIDs := map[domain.ID]struct{}{}
	seenInbounds := map[string]struct{}{}
	ownedRules := make([]json.RawMessage, 0, len(normalized))
	review := FragmentReview{
		Engine: "xray", ServiceName: installation.ServiceName, ManagedPath: installation.ManagedPath,
		ForeignStateHash: foreign.Hash, FragmentStateHash: fragment.Hash, Actions: []FragmentAction{},
	}
	for _, binding := range normalized {
		if err := binding.Validate(); err != nil {
			return FragmentExecutionPlan{}, fmt.Errorf("validate Xray binding %q: %w", binding.ID, err)
		}
		if _, exists := seenIDs[binding.ID]; exists {
			return FragmentExecutionPlan{}, fmt.Errorf("duplicate Xray binding %q", binding.ID)
		}
		seenIDs[binding.ID] = struct{}{}
		if !binding.Enabled {
			continue
		}
		if _, exists := availableInbounds[binding.InboundTag]; !exists {
			return FragmentExecutionPlan{}, fmt.Errorf("Xray binding %q references a missing inbound tag", binding.ID)
		}
		if _, exists := availableOutbounds[binding.OutboundTag]; !exists {
			return FragmentExecutionPlan{}, fmt.Errorf("Xray binding %q references a missing outbound tag", binding.ID)
		}
		if _, exists := seenInbounds[binding.InboundTag]; exists {
			return FragmentExecutionPlan{}, fmt.Errorf("Xray inbound tag %q has multiple enabled bindings", binding.InboundTag)
		}
		seenInbounds[binding.InboundTag] = struct{}{}
		rule, marshalErr := json.Marshal(map[string]any{
			"type": "field", "ruleTag": ownedRulePrefix + string(binding.ID),
			"inboundTag": []string{binding.InboundTag}, "outboundTag": binding.OutboundTag,
		})
		if marshalErr != nil {
			return FragmentExecutionPlan{}, fmt.Errorf("encode Xray binding %q: %w", binding.ID, marshalErr)
		}
		ownedRules = append(ownedRules, rule)
		review.EnabledBindings++
		review.Actions = append(review.Actions, FragmentAction{Kind: "route", Resource: string(binding.ID), Summary: "Prepend one native inboundTag-to-outboundTag rule."})
	}
	if len(ownedRules) == 0 {
		review.CandidateHash = hashFragmentState(nil, false)
		if fragment.Exists {
			review.Actions = append([]FragmentAction{{Kind: "delete", Resource: installation.ManagedPath, Summary: "Remove the owned Xray routing fragment."}}, review.Actions...)
		}
		return FragmentExecutionPlan{Review: review}, nil
	}
	routing["rules"], err = json.Marshal(append(ownedRules, foreignRules...))
	if err != nil {
		return FragmentExecutionPlan{}, fmt.Errorf("encode Xray routing rules: %w", err)
	}
	routingBytes, err := json.Marshal(routing)
	if err != nil {
		return FragmentExecutionPlan{}, fmt.Errorf("encode Xray routing object: %w", err)
	}
	candidate, err := json.MarshalIndent(struct {
		Routing json.RawMessage `json:"routing"`
	}{Routing: routingBytes}, "", "  ")
	if err != nil {
		return FragmentExecutionPlan{}, fmt.Errorf("encode Xray fragment: %w", err)
	}
	candidate = append(candidate, '\n')
	if len(candidate) > MaximumConfigurationBytes {
		return FragmentExecutionPlan{}, fmt.Errorf("Xray fragment candidate exceeds %d bytes", MaximumConfigurationBytes)
	}
	review.CandidateExists = true
	review.CandidateHash = hashFragmentState(candidate, true)
	kind := "create"
	if fragment.Exists {
		kind = "replace"
	}
	review.Actions = append([]FragmentAction{{Kind: kind, Resource: installation.ManagedPath, Summary: "Atomically install the dedicated Xray routing fragment."}}, review.Actions...)
	return FragmentExecutionPlan{Review: review, candidate: candidate}, nil
}

func validateManagedInstallation(installation Installation) error {
	if installation.Kind != Standalone || installation.Ownership != ForeignOwnership || installation.MutationStrategy != ManagedFragment || installation.Loader != "confdir" {
		return fmt.Errorf("Xray installation does not have a proven managed-fragment boundary")
	}
	if !filepath.IsAbs(installation.ConfigRoot) || filepath.Clean(installation.ManagedPath) != filepath.Join(filepath.Clean(installation.ConfigRoot), managedFragmentName) {
		return fmt.Errorf("Xray managed fragment path is invalid")
	}
	return nil
}

func decodeRouting(content []byte, ownedFragment bool) (map[string]json.RawMessage, []json.RawMessage, error) {
	if len(content) == 0 {
		return map[string]json.RawMessage{}, []json.RawMessage{}, nil
	}
	var routing map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(&routing); err != nil {
		return nil, nil, fmt.Errorf("routing object is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, nil, fmt.Errorf("routing object has trailing data")
	}
	if routing == nil {
		return nil, nil, fmt.Errorf("routing value must be an object")
	}
	rules := []json.RawMessage{}
	if rawRules, exists := routing["rules"]; exists {
		if err := json.Unmarshal(rawRules, &rules); err != nil || len(rules) > 4096 {
			return nil, nil, fmt.Errorf("routing rules are invalid or exceed 4096")
		}
	}
	foundOwned := false
	foreignSeen := false
	seenOwnedIDs := map[domain.ID]struct{}{}
	seenOwnedInbounds := map[string]struct{}{}
	for index, rawRule := range rules {
		var identity struct {
			RuleTag string `json:"ruleTag"`
		}
		if err := json.Unmarshal(rawRule, &identity); err != nil {
			return nil, nil, fmt.Errorf("routing rule %d is invalid", index)
		}
		isOwned := strings.HasPrefix(identity.RuleTag, ownedRulePrefix)
		if ownedFragment {
			if isOwned {
				if foreignSeen {
					return nil, nil, fmt.Errorf("owned Xray rules are not a contiguous prefix")
				}
				var owned map[string]json.RawMessage
				if err := json.Unmarshal(rawRule, &owned); err != nil || len(owned) != 4 {
					return nil, nil, fmt.Errorf("owned Xray rule %d has an invalid shape", index)
				}
				var kind, ruleTag, outboundTag string
				var inboundTags []string
				if err := json.Unmarshal(owned["type"], &kind); err != nil || kind != "field" {
					return nil, nil, fmt.Errorf("owned Xray rule %d has an invalid type", index)
				}
				if err := json.Unmarshal(owned["ruleTag"], &ruleTag); err != nil || ruleTag != identity.RuleTag {
					return nil, nil, fmt.Errorf("owned Xray rule %d has an invalid ruleTag", index)
				}
				id := domain.ID(strings.TrimPrefix(ruleTag, ownedRulePrefix))
				if err := id.Validate("owned Xray rule id"); err != nil {
					return nil, nil, fmt.Errorf("owned Xray rule %d has an invalid id", index)
				}
				if err := json.Unmarshal(owned["inboundTag"], &inboundTags); err != nil || len(inboundTags) != 1 || !validTag(inboundTags[0]) {
					return nil, nil, fmt.Errorf("owned Xray rule %d has invalid inboundTag", index)
				}
				if err := json.Unmarshal(owned["outboundTag"], &outboundTag); err != nil || !validTag(outboundTag) {
					return nil, nil, fmt.Errorf("owned Xray rule %d has invalid outboundTag", index)
				}
				if _, exists := seenOwnedIDs[id]; exists {
					return nil, nil, fmt.Errorf("owned Xray fragment has duplicate rule ids")
				}
				if _, exists := seenOwnedInbounds[inboundTags[0]]; exists {
					return nil, nil, fmt.Errorf("owned Xray fragment has duplicate inbound tags")
				}
				seenOwnedIDs[id] = struct{}{}
				seenOwnedInbounds[inboundTags[0]] = struct{}{}
				foundOwned = true
			} else {
				foreignSeen = true
			}
		} else if isOwned {
			return nil, nil, fmt.Errorf("foreign routing collides with the reserved ruleTag prefix")
		}
	}
	if ownedFragment && !foundOwned {
		return nil, nil, fmt.Errorf("Xray fragment contains no owned routing rule")
	}
	return routing, rules, nil
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func hashFragmentState(content []byte, exists bool) string {
	hash := sha256.New()
	if exists {
		_, _ = hash.Write([]byte("xray-fragment-present\x00"))
	} else {
		_, _ = hash.Write([]byte("xray-fragment-absent\x00"))
	}
	_, _ = hash.Write(content)
	return hex.EncodeToString(hash.Sum(nil))
}
