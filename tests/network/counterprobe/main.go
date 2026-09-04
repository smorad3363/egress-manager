package main

import (
	"context"
	"encoding/json"
	"log"
	"os"

	"github.com/egress-manager/egress-manager/internal/nat"
	"github.com/egress-manager/egress-manager/internal/system"
)

func main() {
	var snapshot nat.CounterSnapshot
	var err error
	if os.Getenv("EGRESS_COUNTER_ENGINE") == "iptables" {
		snapshot, err = (nat.IPTablesCounterReader{Runner: system.ExecRunner{}}).Read(context.Background(), nat.IPv4)
	} else {
		snapshot, err = (nat.CounterReader{Runner: system.ExecRunner{}}).Read(context.Background(), nat.IPv4)
	}
	if err != nil {
		log.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(snapshot); err != nil {
		log.Fatal(err)
	}
}
