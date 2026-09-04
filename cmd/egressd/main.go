package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/egress-manager/egress-manager/internal/app"
	"github.com/egress-manager/egress-manager/internal/buildinfo"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(arguments []string) int {
	flags := flag.NewFlagSet("egressd", flag.ContinueOnError)
	configPath := flags.String("config", "/etc/egress-manager/config.json", "absolute configuration path")
	keyPath := flags.String("ipc-key", "/etc/egress-manager/ipc.key", "absolute IPC shared-key path")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "egressd: unexpected positional arguments")
		return 2
	}
	if *showVersion {
		fmt.Printf("egressd %s (%s, %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date)
		return 0
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	logger := app.NewLogger(slog.LevelInfo)
	if err := app.RunDaemon(ctx, app.DaemonOptions{ConfigPath: *configPath, KeyPath: *keyPath, Logger: logger}); err != nil {
		logger.Error("egressd stopped", "error", err)
		return 1
	}
	return 0
}
