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
	snapshot, err := (nat.CounterReader{Runner: system.ExecRunner{}}).Read(context.Background(), nat.IPv4)
	if err != nil {
		log.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(snapshot); err != nil {
		log.Fatal(err)
	}
}
