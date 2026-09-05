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
	exitBusy             = 4
)

type statusFetcher func(context.Context, string, string) (reliability.RecoveryStatus, error)
type recoveryRunner func(context.Context, string, string) (reliability.RecoveryReport, error)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, fetchStatus, runRecovery))
}

func run(arguments []string, stdout, stderr io.Writer, fetch statusFetcher, recover recoveryRunner) int {
	if len(arguments) == 1 && arguments[0] == "--version" {
		fmt.Fprintf(stdout, "egressctl %s (%s, %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date)
		return exitOK
	}
	if len(arguments) == 0 {
		printUsage(stderr)
		return exitUsage
	}
	switch arguments[0] {
	case "status":
		return runStatus(arguments[1:], stdout, stderr, fetch)
	case "recover":
		return runRecover(arguments[1:], stdout, stderr, recover)
	default:
		printUsage(stderr)
		return exitUsage
	}
}

func runStatus(arguments []string, stdout, stderr io.Writer, fetch statusFetcher) int {
	flags := flag.NewFlagSet("egressctl status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "/etc/egress-manager/config.json", "absolute configuration path")
	keyPath := flags.String("ipc-key", "/etc/egress-manager/ipc.key", "absolute IPC shared-key path")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(arguments); err != nil {
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

func runRecover(arguments []string, stdout, stderr io.Writer, recover recoveryRunner) int {
	flags := flag.NewFlagSet("egressctl recover", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "/etc/egress-manager/config.json", "absolute configuration path")
	keyPath := flags.String("ipc-key", "/etc/egress-manager/ipc.key", "absolute IPC shared-key path")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(arguments); err != nil {
		return exitUsage
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "egressctl recover: unexpected positional arguments")
		return exitUsage
	}
	report, err := recover(context.Background(), *configPath, *keyPath)
	if err != nil {
		fmt.Fprintf(stderr, "egressctl recover: %v\n", err)
		if ipc.IsRemoteError(err, "busy") {
			return exitBusy
		}
		return exitFailure
	}
	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintf(stderr, "egressctl recover: encode output: %v\n", err)
			return exitFailure
		}
	} else {
		printRecovery(stdout, report)
	}
	if !report.Succeeded {
		return exitFailure
	}
	return exitOK
}

func fetchStatus(ctx context.Context, configPath, keyPath string) (reliability.RecoveryStatus, error) {
	var status reliability.RecoveryStatus
	if err := callDaemon(ctx, configPath, keyPath, ipc.OperationRecoveryStatus, &status); err != nil {
		return reliability.RecoveryStatus{}, err
	}
	return status, nil
}

func runRecovery(ctx context.Context, configPath, keyPath string) (reliability.RecoveryReport, error) {
	var report reliability.RecoveryReport
	if err := callDaemon(ctx, configPath, keyPath, ipc.OperationRecoveryRun, &report); err != nil {
		return reliability.RecoveryReport{}, err
	}
	return report, nil
}

func callDaemon(ctx context.Context, configPath, keyPath string, operation ipc.Operation, output any) error {
	configuration, err := config.Load(configPath)
	if err != nil {
		return err
	}
	key, err := config.LoadSharedKey(keyPath)
	if err != nil {
		return err
	}
	authenticator, err := ipc.NewAuthenticator(key)
	if err != nil {
		return err
	}
	client := ipc.Client{SocketPath: configuration.ControlSocketPath, Authenticator: authenticator, Timeout: 5 * time.Second}
	return client.Call(ctx, operation, struct{}{}, output)
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
	fmt.Fprintf(writer, "dependencies: %d\n", len(status.Dependencies))
	for _, dependency := range status.Dependencies {
		fmt.Fprintf(writer, "- %s: %s failures %d checked %s\n", dependency.Name, dependency.Status, dependency.ConsecutiveFailures, dependency.LastChecked.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(writer, "observed at: %s\n", status.ObservedAt.UTC().Format(time.RFC3339))
}

func printRecovery(writer io.Writer, report reliability.RecoveryReport) {
	state := "failed"
	if report.Succeeded {
		state = "succeeded"
	}
	fmt.Fprintf(writer, "recovery: %s\n", state)
	for _, step := range report.Steps {
		stepState := "failed"
		if step.Succeeded {
			stepState = "succeeded"
		}
		fmt.Fprintf(writer, "- %s: %s\n", step.Component, stepState)
	}
	if report.FailedComponent != "" {
		fmt.Fprintf(writer, "failed component: %s\n", report.FailedComponent)
	}
	fmt.Fprintf(writer, "started at: %s\n", report.StartedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(writer, "ended at: %s\n", report.EndedAt.UTC().Format(time.RFC3339))
}

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: egressctl <status|recover> [--config PATH] [--ipc-key PATH] [--json]")
}
