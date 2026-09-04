// Package xray provides bounded adapters for externally managed Xray installations.
package xray

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egress-manager/egress-manager/internal/system"
)

const MaximumConfigurationBytes = 256 << 10

const managedFragmentName = "zzzz-egress-manager-routing.json"

type InstallationKind string

const (
	Standalone InstallationKind = "standalone"
	Marzban    InstallationKind = "marzban"
	ThreeXUI   InstallationKind = "3x-ui"
)

type MutationStrategy string

const (
	ReadOnly        MutationStrategy = "read_only"
	ManagedFragment MutationStrategy = "managed_fragment"
)

type OwnershipStatus string

const ForeignOwnership OwnershipStatus = "foreign"

type FileMetadata struct {
	Exists  bool
	Regular bool
	Symlink bool
	Size    int64
}

type FileSystem interface {
	Inspect(string) (FileMetadata, error)
	ReadFile(string) ([]byte, error)
}

type OSFileSystem struct{}

func (OSFileSystem) Inspect(path string) (FileMetadata, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return FileMetadata{}, nil
	}
	if err != nil {
		return FileMetadata{}, err
	}
	return FileMetadata{Exists: true, Regular: info.Mode().IsRegular(), Symlink: info.Mode()&os.ModeSymlink != 0, Size: info.Size()}, nil
}

func (OSFileSystem) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

type Installation struct {
	Kind             InstallationKind `json:"kind"`
	ConfigPath       string           `json:"config_path"`
	ConfigRoot       string           `json:"config_root"`
	ConfigHash       string           `json:"config_hash"`
	ConfigBytes      int              `json:"config_bytes"`
	XrayExecutable   string           `json:"xray_executable,omitempty"`
	XrayVersion      string           `json:"xray_version,omitempty"`
	ServiceName      string           `json:"service_name"`
	ServiceLoaded    bool             `json:"service_loaded"`
	ServiceActive    bool             `json:"service_active"`
	Loader           string           `json:"loader"`
	ManagedPath      string           `json:"managed_path,omitempty"`
	InboundTags      []string         `json:"inbound_tags"`
	OutboundTags     []string         `json:"outbound_tags"`
	Ownership        OwnershipStatus  `json:"ownership"`
	MutationStrategy MutationStrategy `json:"mutation_strategy"`
	Limitations      []string         `json:"limitations"`
}

type Report struct {
	Installations []Installation `json:"installations"`
	Warnings      []string       `json:"warnings"`
}

type Discoverer struct {
	Runner  system.Runner
	Files   FileSystem
	Timeout time.Duration
}

type candidate struct {
	kind     InstallationKind
	path     string
	service  string
	binaries []string
}

var knownCandidates = []candidate{
	{Marzban, "/var/lib/marzban/xray_config.json", "marzban.service", []string{"/var/lib/marzban/xray-core/xray", "/usr/local/bin/xray", "xray"}},
	{ThreeXUI, "/usr/local/x-ui/bin/config.json", "x-ui.service", []string{"/usr/local/x-ui/bin/xray-linux-amd64"}},
	{Standalone, "/etc/xray/config.json", "xray.service", []string{"xray", "/usr/local/bin/xray"}},
	{Standalone, "/usr/local/etc/xray/config.json", "xray.service", []string{"xray", "/usr/local/bin/xray"}},
}

func (discoverer Discoverer) Discover(ctx context.Context) (Report, error) {
	if discoverer.Runner == nil || discoverer.Files == nil {
		return Report{}, fmt.Errorf("Xray discovery runner and file system are required")
	}
	timeout := discoverer.Timeout
	if timeout <= 0 || timeout > 10*time.Second {
		timeout = 2 * time.Second
	}
	report := Report{Installations: []Installation{}, Warnings: []string{}}
	seenPaths := map[string]struct{}{}
	for _, item := range knownCandidates {
		metadata, err := discoverer.Files.Inspect(item.path)
		if err != nil {
			report.Warnings = append(report.Warnings, "unable to inspect a known Xray configuration path")
			continue
		}
		if !metadata.Exists {
			continue
		}
		if !metadata.Regular || metadata.Symlink || metadata.Size < 2 || metadata.Size > MaximumConfigurationBytes {
			report.Warnings = append(report.Warnings, "rejected unsafe or oversized Xray configuration at "+item.path)
			continue
		}
		if _, exists := seenPaths[item.path]; exists {
			continue
		}
		seenPaths[item.path] = struct{}{}
		content, err := discoverer.Files.ReadFile(item.path)
		if err != nil || int64(len(content)) != metadata.Size || len(content) > MaximumConfigurationBytes {
			report.Warnings = append(report.Warnings, "unable to read a stable Xray configuration at "+item.path)
			continue
		}
		inboundTags, outboundTags, err := parseTags(content)
		if err != nil {
			report.Warnings = append(report.Warnings, "rejected malformed Xray configuration at "+item.path)
			continue
		}
		loaded, active, arguments := discoverer.serviceEvidence(ctx, timeout, item.service)
		strategy, loader, managedPath := ReadOnly, "single_file_or_unproven", ""
		if loaded {
			strategy, loader, managedPath = classifyLoader(item.kind, item.path, arguments)
		}
		executable, version := discoverer.xrayVersion(ctx, timeout, item.binaries)
		digest := sha256.Sum256(content)
		report.Installations = append(report.Installations, Installation{
			Kind: item.kind, ConfigPath: item.path, ConfigRoot: filepath.Dir(item.path), ConfigHash: hex.EncodeToString(digest[:]), ConfigBytes: len(content),
			XrayExecutable: executable, XrayVersion: version,
			ServiceName: item.service, ServiceLoaded: loaded, ServiceActive: active, Loader: loader, ManagedPath: managedPath,
			InboundTags: inboundTags, OutboundTags: outboundTags, Ownership: ForeignOwnership,
			MutationStrategy: strategy, Limitations: limitations(item.kind, strategy),
		})
	}
	report.Installations, report.Warnings = rejectAmbiguousServices(report.Installations, report.Warnings)
	sort.Slice(report.Installations, func(i, j int) bool {
		if report.Installations[i].Kind != report.Installations[j].Kind {
			return report.Installations[i].Kind < report.Installations[j].Kind
		}
		return report.Installations[i].ConfigPath < report.Installations[j].ConfigPath
	})
	report.Warnings = uniqueSorted(report.Warnings)
	return report, nil
}

func (discoverer Discoverer) xrayVersion(ctx context.Context, timeout time.Duration, binaries []string) (string, string) {
	for _, binary := range binaries {
		output, err := discoverer.run(ctx, timeout, system.Command{Name: binary, Args: []string{"version"}})
		if err != nil {
			continue
		}
		line := strings.TrimSpace(strings.SplitN(string(output), "\n", 2)[0])
		if line != "" && len(line) <= 160 {
			return binary, line
		}
	}
	return "", ""
}

func (discoverer Discoverer) serviceEvidence(ctx context.Context, timeout time.Duration, service string) (bool, bool, []string) {
	output, err := discoverer.run(ctx, timeout, system.Command{Name: "systemctl", Args: []string{"show", "--property=LoadState,ActiveState,ExecStart", service}})
	if err != nil {
		return false, false, nil
	}
	properties := map[string]string{}
	for _, line := range strings.Split(string(output), "\n") {
		key, value, found := strings.Cut(line, "=")
		if found {
			properties[key] = value
		}
	}
	return properties["LoadState"] == "loaded", properties["ActiveState"] == "active", parseExecStart(properties["ExecStart"])
}

func parseExecStart(value string) []string {
	if strings.Count(value, "argv[]=") != 1 || strings.ContainsAny(value, "\r\n\x00\\\"'") {
		return nil
	}
	_, remainder, found := strings.Cut(value, "argv[]=")
	if !found {
		return nil
	}
	argumentsText, _, found := strings.Cut(remainder, " ;")
	if !found {
		return nil
	}
	arguments := strings.Fields(argumentsText)
	if len(arguments) < 2 || len(arguments) > 32 {
		return nil
	}
	for _, argument := range arguments {
		if len(argument) > 512 || strings.ContainsAny(argument, ";\t") {
			return nil
		}
	}
	return arguments
}

func classifyLoader(kind InstallationKind, configPath string, arguments []string) (MutationStrategy, string, string) {
	if kind != Standalone || len(arguments) < 2 || filepath.Base(arguments[0]) != "xray" || arguments[1] != "run" {
		return ReadOnly, "single_file_or_unproven", ""
	}
	root := filepath.Clean(filepath.Dir(configPath))
	matchedRoot := ""
	for index := 2; index < len(arguments); index++ {
		if arguments[index] == "-c" || arguments[index] == "-config" || strings.HasPrefix(arguments[index], "-c=") || strings.HasPrefix(arguments[index], "-config=") {
			return ReadOnly, "single_file_or_unproven", ""
		}
		var value string
		switch {
		case arguments[index] == "-confdir" && index+1 < len(arguments):
			value = arguments[index+1]
		case strings.HasPrefix(arguments[index], "-confdir="):
			value = strings.TrimPrefix(arguments[index], "-confdir=")
		}
		if value == "" {
			continue
		}
		if matchedRoot != "" || !filepath.IsAbs(value) || filepath.Clean(value) != root {
			return ReadOnly, "single_file_or_unproven", ""
		}
		matchedRoot = root
	}
	if matchedRoot != "" {
		return ManagedFragment, "confdir", filepath.Join(root, managedFragmentName)
	}
	return ReadOnly, "single_file_or_unproven", ""
}

func (discoverer Discoverer) run(ctx context.Context, timeout time.Duration, command system.Command) ([]byte, error) {
	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := discoverer.Runner.Run(commandContext, command)
	if err != nil || result.ExitCode != 0 {
		return nil, errors.Join(err, fmt.Errorf("discovery probe exited with code %d", result.ExitCode))
	}
	return result.Stdout, nil
}

type tagDocument struct {
	Inbounds  []taggedObject `json:"inbounds"`
	Outbounds []taggedObject `json:"outbounds"`
}

type taggedObject struct {
	Tag string `json:"tag"`
}

func parseTags(content []byte) ([]string, []string, error) {
	var document tagDocument
	decoder := json.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(&document); err != nil {
		return nil, nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, nil, fmt.Errorf("Xray configuration contains trailing data")
	}
	inbounds, err := normalizeTags(document.Inbounds, "inbound")
	if err != nil {
		return nil, nil, err
	}
	outbounds, err := normalizeTags(document.Outbounds, "outbound")
	if err != nil {
		return nil, nil, err
	}
	return inbounds, outbounds, nil
}

func normalizeTags(items []taggedObject, kind string) ([]string, error) {
	if len(items) > 4096 {
		return nil, fmt.Errorf("Xray %s count exceeds 4096", kind)
	}
	seen := make(map[string]struct{}, len(items))
	tags := make([]string, 0, len(items))
	for _, item := range items {
		if !validTag(item.Tag) {
			return nil, fmt.Errorf("Xray %s tag is invalid", kind)
		}
		if _, exists := seen[item.Tag]; exists {
			return nil, fmt.Errorf("Xray %s tag is duplicated", kind)
		}
		seen[item.Tag] = struct{}{}
		tags = append(tags, item.Tag)
	}
	sort.Strings(tags)
	return tags, nil
}

func validTag(tag string) bool {
	if tag == "" || tag != strings.TrimSpace(tag) || len(tag) > 128 {
		return false
	}
	for _, character := range tag {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func limitations(kind InstallationKind, strategy MutationStrategy) []string {
	common := "Read-only until the active loader proves an isolated managed-fragment or documented API boundary."
	switch kind {
	case Marzban:
		return []string{common, "Marzban owns and may regenerate xray_config.json; direct file mutation is disabled."}
	case ThreeXUI:
		return []string{common, "3x-ui owns runtime configuration and database state; direct file or database mutation is disabled."}
	default:
		if strategy == ManagedFragment {
			return []string{"Only the dedicated Egress Manager routing fragment may be changed; all other Xray files remain foreign-owned."}
		}
		return []string{common, "The standalone systemd command line and include semantics have not yet been proven."}
	}
}

func rejectAmbiguousServices(installations []Installation, warnings []string) ([]Installation, []string) {
	counts := make(map[string]int, len(installations))
	for _, installation := range installations {
		counts[installation.ServiceName]++
	}
	result := installations[:0]
	for _, installation := range installations {
		if counts[installation.ServiceName] > 1 {
			warnings = append(warnings, "rejected ambiguous Xray configuration candidates for "+installation.ServiceName)
			continue
		}
		result = append(result, installation)
	}
	return result, warnings
}

func uniqueSorted(values []string) []string {
	sort.Strings(values)
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}
