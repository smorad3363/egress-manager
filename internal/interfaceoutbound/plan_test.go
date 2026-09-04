package interfaceoutbound

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/inventory"
)

func TestBuildPlanIsDeterministicOwnedAndPubliclyRedacted(t *testing.T) {
	t.Parallel()
	wireGuard, err := ParseImport(readFixture(t, "wireguard.conf"))
	if err != nil {
		t.Fatal(err)
	}
	openVPN, err := ParseImport(readFixture(t, "client.ovpn"))
	if err != nil {
		t.Fatal(err)
	}
	state, _ := ParseState(nil, false)
	outbounds := []domain.Outbound{openVPN.Outbound, wireGuard.Outbound}
	credentials := map[domain.ID][]byte{wireGuard.Outbound.ID: wireGuard.CredentialDocument, openVPN.Outbound.ID: openVPN.CredentialDocument}
	first, err := BuildPlan(outbounds, credentials, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildPlan([]domain.Outbound{wireGuard.Outbound, openVPN.Outbound}, credentials, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Review.CandidateHash != second.Review.CandidateHash || string(first.Candidate()) != string(second.Candidate()) || first.Review.EnabledOutbounds != 2 {
		t.Fatalf("plans are not deterministic: %#v %#v", first.Review, second.Review)
	}
	public, err := json.Marshal(struct {
		Review    Review `json:"review"`
		Candidate []byte `json:"candidate"`
	}{Review: first.Review, Candidate: first.Candidate()})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"AQIDBAUG", "TEST_ONLY_PRIVATE_KEY", "TEST_ONLY_PASSWORD"} {
		if strings.Contains(string(public), secret) {
			t.Fatalf("public plan leaked credential %q", secret)
		}
	}
	if !strings.Contains(string(first.Candidate()), ownedStateSchema) {
		t.Fatalf("candidate omitted ownership schema: %s", first.Candidate())
	}
}

func TestInspectStateAuthenticatesRuntimeConfigurations(t *testing.T) {
	t.Parallel()
	result, err := ParseImport(readFixture(t, "wireguard.conf"))
	if err != nil {
		t.Fatal(err)
	}
	absent, _ := ParseState(nil, false)
	plan, err := BuildPlan([]domain.Outbound{result.Outbound}, map[domain.ID][]byte{result.Outbound.ID: result.CredentialDocument}, absent, nil)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	runtimeDirectory := filepath.Join(root, "runtime")
	if err := os.Mkdir(runtimeDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "state.json")
	if err := os.WriteFile(statePath, plan.Candidate(), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, entry := range plan.entries {
		if err := os.WriteFile(runtimeConfigPath(runtimeDirectory, entry.state), entry.config, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	state, err := InspectState(statePath, runtimeDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if state.Hash != plan.Review.CandidateHash || len(state.Entries) != 1 {
		t.Fatalf("state = %#v, review = %#v", state, plan.Review)
	}
	if err := os.WriteFile(runtimeConfigPath(runtimeDirectory, plan.entries[0].state), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectState(statePath, runtimeDirectory); err == nil {
		t.Fatal("changed runtime credential accepted")
	}
}

func TestBuildPlanRejectsForeignInterfaceCollisionAndCredentialMismatch(t *testing.T) {
	t.Parallel()
	result, err := ParseImport(readFixture(t, "wireguard.conf"))
	if err != nil {
		t.Fatal(err)
	}
	state, _ := ParseState(nil, false)
	credential, err := DecodeCredential(result.CredentialDocument)
	if err != nil {
		t.Fatal(err)
	}
	interfaces := []inventory.Interface{{Name: credential.InterfaceName, State: "up"}}
	if _, err := BuildPlan([]domain.Outbound{result.Outbound}, map[domain.ID][]byte{result.Outbound.ID: result.CredentialDocument}, state, interfaces); err == nil {
		t.Fatal("foreign interface collision accepted")
	}
	mismatch := result.Outbound
	mismatch.Server.Host = "other.example.com"
	if _, err := BuildPlan([]domain.Outbound{mismatch}, map[domain.ID][]byte{mismatch.ID: result.CredentialDocument}, state, nil); err == nil {
		t.Fatal("credential endpoint mismatch accepted")
	}
}

func TestBuildPlanIgnoresDisabledAndOtherAdapters(t *testing.T) {
	t.Parallel()
	native, err := ParseImport(readFixture(t, "wireguard.conf"))
	if err != nil {
		t.Fatal(err)
	}
	native.Outbound.Enabled = false
	singBox := native.Outbound
	singBox.ID = "legacy_wg"
	singBox.Adapter = domain.OutboundAdapterSingBox
	state, _ := ParseState(nil, false)
	plan, err := BuildPlan([]domain.Outbound{native.Outbound, singBox}, nil, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Review.EnabledOutbounds != 0 || plan.Review.CandidateExists || len(plan.Candidate()) != 0 {
		t.Fatalf("unexpected empty plan: %#v", plan.Review)
	}
}
