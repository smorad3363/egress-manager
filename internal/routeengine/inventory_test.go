package routeengine

import (
	"context"
	"testing"

	"github.com/egress-manager/egress-manager/internal/system"
)

type linkInventoryRunner struct{ output string }

func (runner linkInventoryRunner) Run(context.Context, system.Command) (system.Result, error) {
	return system.Result{Stdout: []byte(runner.output)}, nil
}

func TestVerifyTUNParsesLinkInventory(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, input string
		valid       bool
	}{
		{"spaced", `[ { "ifname" : "egm_test" } ]`, true},
		{"missing", `[]`, false},
		{"wrong link", `[{"ifname":"foreign"}]`, false},
		{"nested", `[{"metadata":{"ifname":"egm_test"}}]`, false},
		{"truncated", `[{"ifname":"egm_test"}`, false},
		{"ambiguous", `[{"ifname":"egm_test"},{"ifname":"foreign"}]`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor := Executor{Runner: linkInventoryRunner{output: test.input}}
			if err := executor.verifyTUN(context.Background(), "egm_test"); (err == nil) != test.valid {
				t.Fatalf("verification error=%v", err)
			}
		})
	}
}

func TestInspectOwnedProtocol(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, input    string
		owned, invalid bool
	}{
		{"compact", `[{"protocol":"242"}]`, true, false},
		{"spaced", `[ { "protocol" : "242" } ]`, true, false},
		{"numeric", `[{"protocol":242}]`, true, false},
		{"foreign", `[{"protocol":"boot"},{"protocol":243}]`, false, false},
		{"nested is not ownership", `[{"metadata":{"protocol":"242"}}]`, false, false},
		{"empty", `[]`, false, false},
		{"truncated", `[{"protocol":"242"}`, false, true},
		{"trailing", `[{"protocol":"242"}] garbage`, false, true},
		{"null", `null`, false, true},
		{"object", `{"protocol":"242"}`, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			owned, err := inspectOwnedProtocol([]byte(test.input))
			if owned != test.owned || (err != nil) != test.invalid {
				t.Fatalf("owned=%v error=%v", owned, err)
			}
		})
	}
}
