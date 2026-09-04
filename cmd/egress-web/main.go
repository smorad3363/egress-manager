package main

import (
	"fmt"

	"github.com/egress-manager/egress-manager/internal/buildinfo"
)

func main() {
	fmt.Printf("egress-web %s (%s, %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date)
}
