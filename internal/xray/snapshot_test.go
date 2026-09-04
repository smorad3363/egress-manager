package xray

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestSnapshotConfdirHashesForeignFilesAndFindsEffectiveState(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "01-base.json"), `{"inbounds":[{"tag":"in-b"},{"tag":"in-a"}],"outbounds":[{"tag":"direct"}],"routing":{"domainStrategy":"AsIs","rules":[{"outboundTag":"direct"}]}}`)
	writeTestFile(t, filepath.Join(root, "20-proxy.json"), `{"outbounds":[{"tag":"proxy"}],"routing":{"domainStrategy":"IPIfNonMatch","rules":[]}}`)
	installation := tempManagedInstallation(root, filepath.Join(root, "01-base.json"))
	snapshot, err := SnapshotConfdir(installation)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Foreign.Hash == "" || snapshot.Fragment.Exists || !slices.Equal(snapshot.InboundTags, []string{"in-a", "in-b"}) || !slices.Equal(snapshot.OutboundTags, []string{"direct", "proxy"}) {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	plan, err := BuildFragmentPlan(snapshot.InstallationWithEffectiveTags(installation), []Binding{{ID: "route", InboundTag: "in-a", OutboundTag: "proxy", Enabled: true}}, snapshot.Foreign, snapshot.Fragment)
	if err != nil || !plan.Review.CandidateExists {
		t.Fatalf("plan = %#v, err = %v", plan, err)
	}
	writeTestFile(t, installation.ManagedPath, string(plan.Candidate()))
	next, err := SnapshotConfdir(installation)
	if err != nil {
		t.Fatal(err)
	}
	if !next.Fragment.Exists || next.Fragment.Hash != plan.Review.CandidateHash || next.Foreign.Hash != snapshot.Foreign.Hash {
		t.Fatalf("next snapshot = %#v", next)
	}
}

func TestSnapshotConfdirRejectsUnsafeOrUncomposableLayouts(t *testing.T) {
	t.Parallel()
	t.Run("non JSON", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "config.json"), `{}`)
		writeTestFile(t, filepath.Join(root, "later.yaml"), `routing: {rules: []}`)
		if _, err := SnapshotConfdir(tempManagedInstallation(root, filepath.Join(root, "config.json"))); err == nil {
			t.Fatal("accepted a mixed-format confdir")
		}
	})
	t.Run("loaded after fragment", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "config.json"), `{}`)
		writeTestFile(t, filepath.Join(root, "zzzzz-foreign.json"), `{}`)
		if _, err := SnapshotConfdir(tempManagedInstallation(root, filepath.Join(root, "config.json"))); err == nil {
			t.Fatal("accepted a foreign file loaded after the managed fragment")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "target")
		writeTestFile(t, target, `{}`)
		config := filepath.Join(root, "config.json")
		if err := os.Symlink(target, config); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, err := SnapshotConfdir(tempManagedInstallation(root, config)); err == nil {
			t.Fatal("accepted a symlinked configuration")
		}
	})
}

func tempManagedInstallation(root, config string) Installation {
	return Installation{
		Kind: Standalone, ConfigPath: config, ConfigRoot: root, ServiceName: "xray.service", Loader: "confdir",
		ManagedPath: filepath.Join(root, managedFragmentName), Ownership: ForeignOwnership, MutationStrategy: ManagedFragment,
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
