package xray

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/egress-manager/egress-manager/internal/system"
)

type memoryFiles struct {
	contents map[string][]byte
	unsafe   map[string]FileMetadata
}

func (files memoryFiles) Inspect(path string) (FileMetadata, error) {
	if metadata, exists := files.unsafe[path]; exists {
		return metadata, nil
	}
	content, exists := files.contents[path]
	if !exists {
		return FileMetadata{}, nil
	}
	return FileMetadata{Exists: true, Regular: true, Size: int64(len(content))}, nil
}

func (files memoryFiles) ReadFile(path string) ([]byte, error) {
	content, exists := files.contents[path]
	if !exists {
		return nil, errors.New("not found")
	}
	return append([]byte{}, content...), nil
}

type discoveryRunner struct{}

func (discoveryRunner) Run(_ context.Context, command system.Command) (system.Result, error) {
	if command.Name == "xray" || strings.Contains(command.Name, "xray") {
		return system.Result{Stdout: []byte("Xray 25.8.3\nA unified platform\n"), ExitCode: 0}, nil
	}
	if command.Name == "systemctl" {
		active := strings.Contains(command.Args[len(command.Args)-1], "marzban")
		output := "LoadState=loaded\nActiveState=inactive\n"
		if active {
			output = "LoadState=loaded\nActiveState=active\n"
		}
		return system.Result{Stdout: []byte(output), ExitCode: 0}, nil
	}
	return system.Result{ExitCode: 127}, errors.New("unexpected command")
}

func TestDiscovererReturnsTypedDeterministicReadOnlyEvidence(t *testing.T) {
	t.Parallel()
	marzban := []byte(`{"inbounds":[{"tag":"vpn-in"}],"outbounds":[{"tag":"direct"},{"tag":"proxy-de"}],"routing":{"rules":[]}}`)
	threeXUI := []byte(`{"inbounds":[{"tag":"panel-in"}],"outbounds":[{"tag":"panel-out"}]}`)
	discoverer := Discoverer{Runner: discoveryRunner{}, Files: memoryFiles{contents: map[string][]byte{
		"/var/lib/marzban/xray_config.json": marzban,
		"/usr/local/x-ui/bin/config.json":   threeXUI,
	}}}
	report, err := discoverer.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Installations) != 2 || report.Installations[0].Kind != ThreeXUI || report.Installations[1].Kind != Marzban {
		t.Fatalf("installations = %#v", report.Installations)
	}
	marzbanResult := report.Installations[1]
	if !marzbanResult.ServiceLoaded || !marzbanResult.ServiceActive || marzbanResult.XrayExecutable != "/var/lib/marzban/xray-core/xray" || marzbanResult.XrayVersion != "Xray 25.8.3" || marzbanResult.Ownership != ForeignOwnership || marzbanResult.MutationStrategy != ReadOnly || marzbanResult.ConfigHash == "" || marzbanResult.ConfigRoot != "/var/lib/marzban" {
		t.Fatalf("Marzban result = %#v", marzbanResult)
	}
	if strings.Contains(strings.Join(marzbanResult.Limitations, " "), string(marzban)) {
		t.Fatal("discovery limitation leaked external configuration")
	}
}

func TestDiscovererRejectsAmbiguousConfigurationForOneService(t *testing.T) {
	t.Parallel()
	content := []byte(`{"inbounds":[{"tag":"vpn-in"}],"outbounds":[{"tag":"direct"}]}`)
	report, err := (Discoverer{Runner: discoveryRunner{}, Files: memoryFiles{contents: map[string][]byte{
		"/etc/xray/config.json":           content,
		"/usr/local/etc/xray/config.json": content,
	}}}).Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Installations) != 0 || len(report.Warnings) != 1 || !strings.Contains(report.Warnings[0], "xray.service") {
		t.Fatalf("report = %#v", report)
	}
}

func TestDiscovererRejectsSymlinksMalformedAndDuplicateTags(t *testing.T) {
	t.Parallel()
	files := memoryFiles{
		contents: map[string][]byte{
			"/var/lib/marzban/xray_config.json": []byte(`{"inbounds":[{"tag":"same"},{"tag":"same"}]}`),
			"/etc/xray/config.json":             []byte(`not-json`),
		},
		unsafe: map[string]FileMetadata{"/usr/local/x-ui/bin/config.json": {Exists: true, Symlink: true, Size: 20}},
	}
	report, err := (Discoverer{Runner: discoveryRunner{}, Files: files}).Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Installations) != 0 || len(report.Warnings) != 3 {
		t.Fatalf("report = %#v", report)
	}
}

func TestParseTagsRejectsTrailingDataAndUnsafeTags(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		`{"inbounds":[]} {}`,
		"{\"inbounds\":[{\"tag\":\"bad\\nvalue\"}]}",
	} {
		if _, _, err := parseTags([]byte(input)); err == nil {
			t.Fatalf("parseTags accepted %q", input)
		}
	}
}
