package routing

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/egress-manager/egress-manager/internal/domain"
)

func TestVerifyNFTRuntimeRejectsSemanticDrift(t *testing.T) {
	t.Parallel()
	state, _ := ParseState(nil, false)
	plan, err := BuildPlan(testRoutingSettings(), []domain.Route{testRoute()}, []domain.Outbound{testRoutingOutbound("primary", "198.51.100.20")}, testRoutingHost(), nil, state)
	if err != nil {
		t.Fatal(err)
	}
	state, err = ParseState(plan.Candidate(), true)
	if err != nil {
		t.Fatal(err)
	}
	objects, err := expectedNFTObjects(state.Routes)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := json.Marshal(map[string]any{"nftables": objects})
	content = bytes.ReplaceAll(content, []byte(`"counter":null`), []byte(`"counter":{"packets":123,"bytes":456}`))
	if err := VerifyNFTRuntime(state.Routes, content); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		old, new []byte
	}{
		{"verdict", []byte(`"drop":null`), []byte(`"accept":null`)},
		{"hook", []byte(`"hook":"forward"`), []byte(`"hook":"output"`)},
		{"priority", []byte(`"prio":-5`), []byte(`"prio":5`)},
		{"selector", []byte(`"right":"tun0"`), []byte(`"right":"foreign"`)},
		{"mss", []byte(`"value":1360`), []byte(`"value":1200`)},
		{"counter object", []byte(`"packets":123,"bytes":456`), []byte(`"name":"foreign"`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := bytes.Replace(content, test.old, test.new, 1)
			if bytes.Equal(content, changed) {
				t.Fatal("fixture did not change")
			}
			if VerifyNFTRuntime(state.Routes, changed) == nil {
				t.Fatal("runtime drift accepted")
			}
		})
	}
	if VerifyNFTRuntime(state.Routes, content[:len(content)-1]) == nil {
		t.Fatal("truncated nftables accepted")
	}
	objects = objects[:len(objects)-1]
	missing, _ := json.Marshal(map[string]any{"nftables": objects})
	missing = bytes.ReplaceAll(missing, []byte(`"counter":null`), []byte(`"counter":{"packets":0,"bytes":0}`))
	if VerifyNFTRuntime(state.Routes, missing) == nil {
		t.Fatal("missing kill switch accepted")
	}
}
