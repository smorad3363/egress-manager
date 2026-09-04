package xray

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	MaximumConfdirFiles = 128
	MaximumConfdirBytes = 1 << 20
)

type ConfdirSnapshot struct {
	Foreign      ForeignRoutingState
	Fragment     FragmentState
	InboundTags  []string
	OutboundTags []string
	foreignFiles []confdirFile
	fragmentData []byte
}

type confdirFile struct {
	name    string
	content []byte
}

func (snapshot ConfdirSnapshot) InstallationWithEffectiveTags(installation Installation) Installation {
	installation.InboundTags = append([]string{}, snapshot.InboundTags...)
	installation.OutboundTags = append([]string{}, snapshot.OutboundTags...)
	return installation
}

func SnapshotConfdir(installation Installation) (ConfdirSnapshot, error) {
	if err := validateManagedInstallation(installation); err != nil {
		return ConfdirSnapshot{}, err
	}
	rootInfo, err := os.Lstat(installation.ConfigRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return ConfdirSnapshot{}, fmt.Errorf("Xray confdir is not a real directory")
	}
	entries, err := os.ReadDir(installation.ConfigRoot)
	if err != nil {
		return ConfdirSnapshot{}, fmt.Errorf("read Xray confdir: %w", err)
	}
	loadedFiles := 0
	totalBytes := 0
	configFound := false
	inboundTags := map[string]struct{}{}
	outboundTags := map[string]struct{}{}
	foreignHash := sha256.New()
	_, _ = foreignHash.Write([]byte("xray-foreign-confdir-v1\x00"))
	var effectiveRouting []byte
	fragment := FragmentState{}
	foreignFiles := []confdirFile{}
	var fragmentData []byte
	for _, entry := range entries {
		if !xrayConfigName(entry.Name()) {
			continue
		}
		loadedFiles++
		if loadedFiles > MaximumConfdirFiles {
			return ConfdirSnapshot{}, fmt.Errorf("Xray confdir exceeds %d loaded files", MaximumConfdirFiles)
		}
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			return ConfdirSnapshot{}, fmt.Errorf("Xray confdir contains a loaded non-JSON format unsupported by safe composition")
		}
		path := filepath.Join(installation.ConfigRoot, entry.Name())
		if filepath.Clean(path) == filepath.Clean(installation.ConfigPath) {
			configFound = true
		}
		if entry.Name() > managedFragmentName {
			return ConfdirSnapshot{}, fmt.Errorf("Xray confdir contains a file loaded after the reserved managed fragment")
		}
		content, err := readStableRegular(path)
		if err != nil {
			return ConfdirSnapshot{}, err
		}
		totalBytes += len(content)
		if totalBytes > MaximumConfdirBytes {
			return ConfdirSnapshot{}, fmt.Errorf("Xray confdir exceeds %d bytes", MaximumConfdirBytes)
		}
		if entry.Name() == managedFragmentName {
			fragment, err = ParseFragmentState(content, true)
			if err != nil {
				return ConfdirSnapshot{}, err
			}
			fragmentData = append([]byte{}, content...)
			continue
		}
		writeHashPart(foreignHash, entry.Name(), content)
		foreignFiles = append(foreignFiles, confdirFile{name: entry.Name(), content: append([]byte{}, content...)})
		document, err := decodeConfdirDocument(content)
		if err != nil {
			return ConfdirSnapshot{}, fmt.Errorf("decode Xray confdir file %q: %w", entry.Name(), err)
		}
		for _, item := range document.Inbounds {
			if item.Tag != "" {
				inboundTags[item.Tag] = struct{}{}
			}
		}
		for _, item := range document.Outbounds {
			if item.Tag != "" {
				outboundTags[item.Tag] = struct{}{}
			}
		}
		if len(document.Routing) != 0 && !bytes.Equal(bytes.TrimSpace(document.Routing), []byte("null")) {
			if _, _, err := decodeRouting(document.Routing, false); err != nil {
				return ConfdirSnapshot{}, fmt.Errorf("decode Xray routing in %q: %w", entry.Name(), err)
			}
			effectiveRouting = append([]byte{}, document.Routing...)
		}
	}
	if !configFound {
		return ConfdirSnapshot{}, fmt.Errorf("discovered Xray configuration is absent from its confdir")
	}
	if fragment.Hash == "" {
		fragment, err = ParseFragmentState(nil, false)
		if err != nil {
			return ConfdirSnapshot{}, err
		}
	}
	return ConfdirSnapshot{
		Foreign:      ForeignRoutingState{Hash: hex.EncodeToString(foreignHash.Sum(nil)), Routing: effectiveRouting},
		Fragment:     fragment,
		InboundTags:  sortedKeys(inboundTags),
		OutboundTags: sortedKeys(outboundTags),
		foreignFiles: foreignFiles,
		fragmentData: fragmentData,
	}, nil
}

type confdirDocument struct {
	Inbounds  []taggedObject  `json:"inbounds"`
	Outbounds []taggedObject  `json:"outbounds"`
	Routing   json.RawMessage `json:"routing"`
}

func decodeConfdirDocument(content []byte) (confdirDocument, error) {
	var document confdirDocument
	decoder := json.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(&document); err != nil {
		return confdirDocument{}, fmt.Errorf("configuration is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return confdirDocument{}, fmt.Errorf("configuration has trailing data")
	}
	if _, err := normalizePresentTags(document.Inbounds, "inbound"); err != nil {
		return confdirDocument{}, err
	}
	if _, err := normalizePresentTags(document.Outbounds, "outbound"); err != nil {
		return confdirDocument{}, err
	}
	return document, nil
}

func normalizePresentTags(items []taggedObject, kind string) ([]string, error) {
	filtered := make([]taggedObject, 0, len(items))
	for _, item := range items {
		if item.Tag != "" {
			filtered = append(filtered, item)
		}
	}
	return normalizeTags(filtered, kind)
}

func readStableRegular(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 || before.Size() < 2 || before.Size() > MaximumConfigurationBytes {
		return nil, fmt.Errorf("Xray confdir entry is not a bounded regular file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Xray confdir entry: %w", err)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) || before.Size() != int64(len(content)) || before.ModTime() != after.ModTime() {
		return nil, fmt.Errorf("Xray confdir changed while it was read")
	}
	return content, nil
}

func xrayConfigName(name string) bool {
	lower := strings.ToLower(name)
	for _, suffix := range []string{".json", ".jsonc", ".toml", ".yaml", ".yml"} {
		if strings.HasSuffix(lower, suffix) && len(name) > len(suffix) {
			return true
		}
	}
	return false
}

func writeHashPart(hash interface{ Write([]byte) (int, error) }, name string, content []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(name)))
	_, _ = hash.Write(length[:])
	_, _ = hash.Write([]byte(name))
	binary.BigEndian.PutUint64(length[:], uint64(len(content)))
	_, _ = hash.Write(length[:])
	_, _ = hash.Write(content)
}

func sortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
