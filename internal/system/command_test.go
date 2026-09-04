package system

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestExecRunnerCapturesOutputWithoutShell(t *testing.T) {
	if os.Getenv("EGRESS_COMMAND_HELPER") == "1" {
		fmt.Print("typed-output")
		return
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := (ExecRunner{}).Run(ctx, Command{
		Name: executable,
		Args: []string{"-test.run=TestExecRunnerCapturesOutputWithoutShell"},
		Env:  []string{"EGRESS_COMMAND_HELPER=1"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v; stderr = %s", err, result.Stderr)
	}
	if !strings.Contains(string(result.Stdout), "typed-output") {
		t.Fatalf("stdout = %q", result.Stdout)
	}
}

func TestSafeBufferCapsCapturedOutput(t *testing.T) {
	t.Parallel()

	buffer := &safeBuffer{}
	input := make([]byte, maxCommandOutput+128)
	written, err := buffer.Write(input)
	if err != nil {
		t.Fatal(err)
	}
	if written != len(input) {
		t.Fatalf("Write() = %d, want %d", written, len(input))
	}
	if len(buffer.Bytes()) != maxCommandOutput {
		t.Fatalf("captured %d bytes, want %d", len(buffer.Bytes()), maxCommandOutput)
	}
}
