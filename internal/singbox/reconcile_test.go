package singbox

import (
	"bytes"
	"testing"
)

func TestReconciliationPreservesAppliedConfiguration(t *testing.T) {
	state, _ := ParseState(nil, false)
	plan, err := BuildPlan(nil, nil, state)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := BuildReconciliationPlan(plan.Candidate())
	if err != nil || !bytes.Equal(replayed.Candidate(), plan.Candidate()) || replayed.Plan.CandidateHash != plan.Plan.CandidateHash {
		t.Fatalf("replay error=%v", err)
	}
	if _, err := BuildReconciliationPlan([]byte(`{"outbounds":[]}`)); err == nil {
		t.Fatal("foreign configuration accepted")
	}
}
