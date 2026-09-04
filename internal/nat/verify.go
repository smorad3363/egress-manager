package nat

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/egress-manager/egress-manager/internal/system"
)

type SystemVerifier struct {
	Runner  system.Runner
	Timeout time.Duration
}

func (verifier SystemVerifier) Verify(ctx context.Context, plan Plan) error {
	if verifier.Runner == nil {
		return fmt.Errorf("verification runner is required")
	}
	timeout := verifier.Timeout
	if timeout <= 0 || timeout > 10*time.Second {
		timeout = 2 * time.Second
	}
	parts := strings.Fields(plan.OwnedTable)
	if len(parts) != 2 {
		return fmt.Errorf("invalid owned NAT table")
	}
	if err := verifier.run(ctx, timeout, system.Command{Name: "nft", Args: []string{"list", "table", parts[0], parts[1]}}); err != nil {
		return fmt.Errorf("verify owned NAT table: %w", err)
	}
	for _, target := range plan.Targets {
		address, err := netip.ParseAddr(target.Address)
		if err != nil || address.IsUnspecified() || address.IsLoopback() {
			return fmt.Errorf("invalid NAT verification target")
		}
		if err := verifier.run(ctx, timeout, system.Command{Name: "ip", Args: []string{"route", "get", address.String()}}); err != nil {
			return fmt.Errorf("remote destination %s is unreachable: %w", address, err)
		}
	}
	return nil
}

func (verifier SystemVerifier) run(ctx context.Context, timeout time.Duration, command system.Command) error {
	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := verifier.Runner.Run(commandContext, command)
	if err != nil || result.ExitCode != 0 {
		return errors.Join(err, fmt.Errorf("probe exited with code %d", result.ExitCode))
	}
	return nil
}
