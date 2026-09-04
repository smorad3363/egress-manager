package xray

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egress-manager/egress-manager/internal/system"
)

func TestRepresentativeConfigurationsRemainBoundedAndTyped(t *testing.T) {
	t.Parallel()
	for _, fixture := range []string{"standalone.json", "marzban.json", "3x-ui.json"} {
		content, err := os.ReadFile(filepath.Join("testdata", fixture))
		if err != nil {
			t.Fatal(err)
		}
		inbounds, outbounds, err := parseTags(content)
		if err != nil || len(inbounds) != 1 || len(outbounds) == 0 {
			t.Fatalf("fixture %s: inbounds=%#v outbounds=%#v err=%v", fixture, inbounds, outbounds, err)
		}
	}
}

func TestNativeFragmentAgainstPinnedXray(t *testing.T) {
	binary := os.Getenv("XRAY_NATIVE_BINARY")
	if binary == "" {
		t.Skip("XRAY_NATIVE_BINARY is not set")
	}
	content, err := os.ReadFile(filepath.Join("testdata", "standalone.json"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(configPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	installation := tempManagedInstallation(root, configPath)
	installation.XrayExecutable = binary
	snapshot, err := SnapshotConfdir(installation)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildFragmentPlan(snapshot.InstallationWithEffectiveTags(installation), []Binding{{ID: "native_route", InboundTag: "client-in", OutboundTag: "direct", Enabled: true}}, snapshot.Foreign, snapshot.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	validationRoot, err := materializeConfdir(snapshot.foreignFiles, plan.Candidate(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(validationRoot)
	result, err := (system.ExecRunner{}).Run(context.Background(), system.Command{Name: binary, Args: []string{"run", "-test", "-confdir", validationRoot}, Dir: root})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("native Xray validation failed: exit=%d err=%v stderr=%s stdout=%s", result.ExitCode, err, result.Stderr, result.Stdout)
	}
}
