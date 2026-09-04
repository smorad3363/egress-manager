package singbox

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/egress-manager/egress-manager/internal/domain"
)

func TestBuildPlanIsDeterministicOwnedAndPubliclyRedacted(t *testing.T) {
	t.Parallel()
	imported, err := ParseImport(readFixture(t, "vless.uri"))
	if err != nil {
		t.Fatal(err)
	}
	outbound := imported[0].Outbound
	state, err := ParseState(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	credentials := map[domain.ID][]byte{outbound.ID: imported[0].CredentialDocument}
	first, err := BuildPlan([]domain.Outbound{outbound}, credentials, state)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildPlan([]domain.Outbound{outbound}, credentials, state)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Candidate(), second.Candidate()) || first.Plan.CandidateHash != second.Plan.CandidateHash {
		t.Fatal("sing-box plan is not deterministic")
	}
	if _, err := ParseState(first.Candidate(), true); err != nil {
		t.Fatal(err)
	}
	publicJSON, err := json.Marshal(first.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicJSON), "00000000-0000-4000") || strings.Contains(string(publicJSON), "credential") {
		t.Fatalf("public plan leaked credential data: %s", publicJSON)
	}
	if !strings.Contains(string(first.Candidate()), "00000000-0000-4000") {
		t.Fatal("private candidate did not contain required adapter credential")
	}
}

func TestBuildPlanRejectsCredentialMismatchAndDuplicateIDs(t *testing.T) {
	t.Parallel()
	imports, err := ParseImport(readFixture(t, "trojan.uri"))
	if err != nil {
		t.Fatal(err)
	}
	outbound := imports[0].Outbound
	state, _ := ParseState(nil, false)
	credentials := map[domain.ID][]byte{outbound.ID: imports[0].CredentialDocument}
	mismatch := outbound
	mismatch.Server.Host = "other.example.com"
	if _, err := BuildPlan([]domain.Outbound{mismatch}, credentials, state); err == nil {
		t.Fatal("credential mismatch accepted")
	}
	if _, err := BuildPlan([]domain.Outbound{outbound, outbound}, credentials, state); err == nil {
		t.Fatal("duplicate outbound accepted")
	}
}

func TestParseStateRejectsForeignConfiguration(t *testing.T) {
	t.Parallel()
	foreign := []byte(`{"$schema":"https://sing-box.sagernet.org/schema.json#egress-manager-owned","log":{"disabled":true},"outbounds":[{"type":"direct","tag":"foreign"}]}`)
	if _, err := ParseState(foreign, true); err == nil {
		t.Fatal("foreign sing-box configuration accepted")
	}
	if _, err := ParseState(nil, false); err != nil {
		t.Fatal(err)
	}
}
