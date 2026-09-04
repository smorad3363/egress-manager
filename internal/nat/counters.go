package nat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/system"
)

type ForwardCounters struct {
	ForwardID       domain.ID `json:"forward_id"`
	AcceptedPackets uint64    `json:"accepted_packets"`
	AcceptedBytes   uint64    `json:"accepted_bytes"`
	DroppedPackets  uint64    `json:"dropped_packets"`
	DroppedBytes    uint64    `json:"dropped_bytes"`
}

type CounterSnapshot struct {
	Family AddressFamily     `json:"family"`
	Items  []ForwardCounters `json:"items"`
}

type CounterReader struct {
	Runner  system.Runner
	Timeout time.Duration
}

func (reader CounterReader) Read(ctx context.Context, family AddressFamily) (CounterSnapshot, error) {
	if reader.Runner == nil {
		return CounterSnapshot{}, fmt.Errorf("counter reader runner is required")
	}
	nativeFamily, table, err := family.nativeName()
	if err != nil {
		return CounterSnapshot{}, err
	}
	timeout := reader.Timeout
	if timeout <= 0 || timeout > 10*time.Second {
		timeout = 2 * time.Second
	}
	exists, err := InspectOwnedTable(ctx, reader.Runner, family, timeout)
	if err != nil {
		return CounterSnapshot{}, err
	}
	if !exists {
		return CounterSnapshot{Family: family, Items: []ForwardCounters{}}, nil
	}
	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, runErr := reader.Runner.Run(commandContext, system.Command{Name: "nft", Args: []string{"--json", "list", "table", nativeFamily, table}})
	if runErr != nil || result.ExitCode != 0 {
		return CounterSnapshot{}, errors.Join(runErr, fmt.Errorf("read nftables counters exited with code %d", result.ExitCode))
	}
	items, err := parseNFTCounters(result.Stdout, nativeFamily, table)
	if err != nil {
		return CounterSnapshot{}, err
	}
	return CounterSnapshot{Family: family, Items: items}, nil
}

type nftCounterDocument struct {
	NFTables []struct {
		Rule *struct {
			Family  string `json:"family"`
			Table   string `json:"table"`
			Comment string `json:"comment"`
			Expr    []struct {
				Counter *struct {
					Packets uint64 `json:"packets"`
					Bytes   uint64 `json:"bytes"`
				} `json:"counter,omitempty"`
			} `json:"expr"`
		} `json:"rule,omitempty"`
	} `json:"nftables"`
}

func parseNFTCounters(input []byte, family, table string) ([]ForwardCounters, error) {
	var document nftCounterDocument
	decoder := json.NewDecoder(bytes.NewReader(input))
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode nftables counter output: %w", err)
	}
	items := map[domain.ID]*ForwardCounters{}
	for _, object := range document.NFTables {
		if object.Rule == nil || object.Rule.Family != family || object.Rule.Table != table {
			continue
		}
		forwardID, kind, ok := counterComment(object.Rule.Comment)
		if !ok {
			continue
		}
		var packets, bytes uint64
		for _, expression := range object.Rule.Expr {
			if expression.Counter != nil {
				packets += expression.Counter.Packets
				bytes += expression.Counter.Bytes
			}
		}
		item := items[forwardID]
		if item == nil {
			item = &ForwardCounters{ForwardID: forwardID}
			items[forwardID] = item
		}
		switch kind {
		case "allow":
			item.AcceptedPackets += packets
			item.AcceptedBytes += bytes
		case "source_drop":
			item.DroppedPackets += packets
			item.DroppedBytes += bytes
		}
	}
	ordered := make([]ForwardCounters, 0, len(items))
	for _, item := range items {
		ordered = append(ordered, *item)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ForwardID < ordered[j].ForwardID })
	return ordered, nil
}

func counterComment(comment string) (domain.ID, string, bool) {
	const prefix = "egm_pf_"
	if !strings.HasPrefix(comment, prefix) {
		return "", "", false
	}
	for _, suffix := range []string{"_source_drop", "_allow"} {
		if !strings.HasSuffix(comment, suffix) {
			continue
		}
		id := domain.ID(strings.TrimSuffix(strings.TrimPrefix(comment, prefix), suffix))
		if id.Validate("counter forward id") != nil {
			return "", "", false
		}
		return id, strings.TrimPrefix(suffix, "_"), true
	}
	return "", "", false
}
