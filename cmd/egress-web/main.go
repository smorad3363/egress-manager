package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/egress-manager/egress-manager/internal/app"
	"github.com/egress-manager/egress-manager/internal/buildinfo"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(arguments []string) int {
	if len(arguments) > 0 && arguments[0] == "provision-admin" {
		return runProvision(arguments[1:])
	}
	flags := flag.NewFlagSet("egress-web", flag.ContinueOnError)
	configPath := flags.String("config", "/etc/egress-manager/config.json", "absolute configuration path")
	keyPath := flags.String("ipc-key", "/etc/egress-manager/ipc.key", "absolute IPC shared-key path")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "egress-web: unexpected positional arguments")
		return 2
	}
	if *showVersion {
		fmt.Printf("egress-web %s (%s, %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date)
		return 0
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	logger := app.NewLogger(slog.LevelInfo)
	if err := app.RunWeb(ctx, app.WebOptions{ConfigPath: *configPath, KeyPath: *keyPath, Logger: logger}); err != nil {
		logger.Error("egress-web stopped", "error", err)
		return 1
	}
	return 0
}

func runProvision(arguments []string) int {
	flags := flag.NewFlagSet("egress-web provision-admin", flag.ContinueOnError)
	configPath := flags.String("config", "/etc/egress-manager/config.json", "absolute configuration path")
	keyPath := flags.String("ipc-key", "/etc/egress-manager/ipc.key", "absolute IPC shared-key path")
	username := flags.String("username", "", "administrator username")
	passwordStdin := flags.Bool("password-stdin", false, "read password from standard input")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *username == "" || !*passwordStdin {
		fmt.Fprintln(os.Stderr, "usage: egress-web provision-admin --username USER --password-stdin [--config PATH] [--ipc-key PATH]")
		return 2
	}
	password, err := readPassword(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "egress-web: read password: %v\n", err)
		return 1
	}
	if err := app.ProvisionAdmin(context.Background(), *configPath, *keyPath, *username, password); err != nil {
		fmt.Fprintf(os.Stderr, "egress-web: provision administrator: %v\n", err)
		return 1
	}
	fmt.Println("administrator provisioned")
	return 0
}

func readPassword(reader io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(reader, 1026))
	if err != nil {
		return "", err
	}
	if len(data) > 1025 {
		return "", fmt.Errorf("password input exceeds 1024 bytes")
	}
	password := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if strings.ContainsAny(password, "\r\n") {
		return "", fmt.Errorf("password input must contain one line")
	}
	return password, nil
}
