package nat

import (
	"context"
	"strings"
	"testing"

	"github.com/egress-manager/egress-manager/internal/system"
)

func TestCounterReaderAggregatesOwnedAllowAndDropRules(t *testing.T) {
	t.Parallel()
	fixture := `{"nftables":[
		{"metainfo":{"version":"1.0.9"}},
		{"rule":{"family":"ip","table":"egm_nat4","chain":"forward","expr":[{"counter":{"packets":2,"bytes":160}},{"accept":null}],"comment":"egm_pf_web_tls_allow"}},
		{"rule":{"family":"ip","table":"egm_nat4","chain":"forward","expr":[{"counter":{"packets":3,"bytes":240}},{"accept":null}],"comment":"egm_pf_web_tls_allow"}},
		{"rule":{"family":"ip","table":"egm_nat4","chain":"forward","expr":[{"counter":{"packets":1,"bytes":64}},{"drop":null}],"comment":"egm_pf_web_tls_source_drop"}},
		{"rule":{"family":"ip","table":"foreign","chain":"forward","expr":[{"counter":{"packets":999,"bytes":999}}],"comment":"egm_pf_web_tls_allow"}},
		{"rule":{"family":"ip","table":"egm_nat4","chain":"prerouting","expr":[{"counter":{"packets":8,"bytes":512}}],"comment":"egm_pf_web_tls_dnat"}}
	]}`
	runner := &scriptedRunner{t: t, steps: []runnerStep{{
		command: "nft list tables",
		result:  system.Result{Stdout: []byte("table ip egm_nat4\n"), ExitCode: 0},
	}, {
		command: "nft --json list table ip egm_nat4",
		result:  system.Result{Stdout: []byte(fixture), ExitCode: 0},
	}}}
	snapshot, err := (CounterReader{Runner: runner}).Read(context.Background(), IPv4)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Family != IPv4 || len(snapshot.Items) != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	item := snapshot.Items[0]
	if item.ForwardID != "web_tls" || item.AcceptedPackets != 5 || item.AcceptedBytes != 400 || item.DroppedPackets != 1 || item.DroppedBytes != 64 {
		t.Fatalf("counter = %#v", item)
	}
	runner.assertDone()
}

func TestCounterReaderRejectsMalformedJSONAndUnsupportedFamily(t *testing.T) {
	t.Parallel()
	runner := &scriptedRunner{t: t, steps: []runnerStep{{
		command: "nft list tables",
		result:  system.Result{Stdout: []byte("table ip egm_nat4\n"), ExitCode: 0},
	}, {
		command: "nft --json list table ip egm_nat4",
		result:  system.Result{Stdout: []byte(`{"nftables":`), ExitCode: 0},
	}}}
	if _, err := (CounterReader{Runner: runner}).Read(context.Background(), IPv4); err == nil || !strings.Contains(err.Error(), "decode nftables") {
		t.Fatalf("Read() error = %v", err)
	}
	if _, err := (CounterReader{Runner: runner}).Read(context.Background(), "inet"); err == nil {
		t.Fatal("Read() accepted unsupported family")
	}
	runner.assertDone()
}

func TestCounterReaderReturnsEmptySnapshotWhenOwnedTableIsAbsent(t *testing.T) {
	t.Parallel()
	runner := &scriptedRunner{t: t, steps: []runnerStep{{
		command: "nft list tables",
		result:  system.Result{Stdout: []byte("table inet foreign\n"), ExitCode: 0},
	}}}
	snapshot, err := (CounterReader{Runner: runner}).Read(context.Background(), IPv4)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Family != IPv4 || snapshot.Items == nil || len(snapshot.Items) != 0 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	runner.assertDone()
}
