package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/egress-manager/egress-manager/internal/inventory"
	"github.com/egress-manager/egress-manager/internal/system"
)

func main() {
	collector := inventory.Collector{Runner: system.ExecRunner{}, Files: inventory.OSFiles{}, Timeout: 2 * time.Second}
	result, err := collector.Collect(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "inventory probe failed")
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, "inventory probe encoding failed")
		os.Exit(1)
	}
}
