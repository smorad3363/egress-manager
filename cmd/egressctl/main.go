package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/egress-manager/egress-manager/internal/buildinfo"
	"github.com/egress-manager/egress-manager/internal/config"
	"github.com/egress-manager/egress-manager/internal/ipc"
	"github.com/egress-manager/egress-manager/internal/reliability"
)

const (
	exitOK               = 0
	exitFailure          = 1
	exitUsage            = 2
	exitRecoveryRequired = 3
)

type statusFetcher func(context.Context, string, string) (reliability.RecoveryStatus, error)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, fetchStatus))
}

func run(arguments []string, stdout, stderr io.Writer, fetch statusFetcher) int {
	if len(arguments) == 1 && arguments[0] == "--version" {
		fmt.Fprintf(stdout, "egressctl %s (%s, %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date)
		return exitOK
	}
	if len(arguments) == 0 || arguments[0] != "status" {
		fmt.Fprintln(stderr, "usage: egressctl status [--config PATH] [--ipc-key PATH] [--json]")
		return exitUsage
	}
	flags := flag.NewFlagSet("egressctl status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "/etc/egress-manager/config.json", "absolute configuration path")
	keyPath := flags.String("ipc-key", "/etc/egress-manager/ipc.key", "absolute IPC shared-key path")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(arguments[1:]); err != nil {
		return exitUsage
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "egressctl status: unexpected positional arguments")
		return exitUsage
	}
	status, err := fetch(context.Background(), *configPath, *keyPath)
	if err != nil {
		fmt.Fprintf(stderr, "egressctl status: %v\n", err)
		return exitFailure
	}
	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(status); err != nil {
			fmt.Fprintf(stderr, "egressctl status: encode output: %v\n", err)
			return exitFailure
		}
	} else {
		printStatus(stdout, status)
	}
	if status.RecoveryRequired {
		return exitRecoveryRequired
	}
	return exitOK
}

func fetchStatus(ctx context.Context, configPath, keyPath string) (reliability.RecoveryStatus, error) {
	configuration, err := config.Load(configPath)
	if err != nil {
		return reliability.RecoveryStatus{}, err
	}
	key, err := config.LoadSharedKey(keyPath)
	if err != nil {
		return reliability.RecoveryStatus{}, err
	}
	authenticator, err := ipc.NewAuthenticator(key)
	if err != nil {
		return reliability.RecoveryStatus{}, err
	}
	client := ipc.Client{SocketPath: configuration.ControlSocketPath, Authenticator: authenticator, Timeout: 5 * time.Second}
	var status reliability.RecoveryStatus
	if err := client.Call(ctx, ipc.OperationRecoveryStatus, struct{}{}, &status); err != nil {
		return reliability.RecoveryStatus{}, err
	}
	return status, nil
}

func printStatus(writer io.Writer, status reliability.RecoveryStatus) {
	ready := "yes"
	if !status.Ready {
		ready = "no"
	}
	lockState := "idle"
	if status.MutationLock.Active {
		lockState = "active"
	}
	fmt.Fprintf(writer, "ready: %s\n", ready)
	fmt.Fprintf(writer, "recovery required: %t\n", status.RecoveryRequired)
	fmt.Fprintf(writer, "mutation lock: %s\n", lockState)
	if status.MutationLock.Owner != nil {
		owner := status.MutationLock.Owner
		fmt.Fprintf(writer, "lock owner: %s operation %s pid %d\n", owner.Component, owner.OperationID, owner.PID)
	}
	fmt.Fprintf(writer, "unfinished operations: %d\n", len(status.Unfinished))
	for _, operation := range status.Unfinished {
		fmt.Fprintf(writer, "- %s %s %s updated %s\n", operation.ID, operation.Operation, operation.State, operation.UpdatedAt.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(writer, "observed at: %s\n", status.ObservedAt.UTC().Format(time.RFC3339))
}
