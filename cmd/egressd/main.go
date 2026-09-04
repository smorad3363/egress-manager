package main

import (
	"fmt"

	"github.com/egress-manager/egress-manager/internal/buildinfo"
)

func main() {
	fmt.Printf("egressd %s (%s, %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date)
}
