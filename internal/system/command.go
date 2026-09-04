// Package system provides injectable operating-system boundaries.
package system

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
)

type Command struct {
	Name  string
	Args  []string
	Env   []string
	Dir   string
	Stdin []byte
}

type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

type Runner interface {
	Run(context.Context, Command) (Result, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, command Command) (Result, error) {
	if len(command.Stdin) > maxCommandInput {
		return Result{ExitCode: -1}, fmt.Errorf("command input exceeds %d bytes", maxCommandInput)
	}
	process := exec.CommandContext(ctx, command.Name, command.Args...)
	process.Env = append(os.Environ(), command.Env...)
	process.Dir = command.Dir
	if len(command.Stdin) > 0 {
		process.Stdin = bytes.NewReader(command.Stdin)
	}
	stdout, stderr := &safeBuffer{}, &safeBuffer{}
	process.Stdout = stdout
	process.Stderr = stderr
	err := process.Run()
	result := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if process.ProcessState != nil {
		result.ExitCode = process.ProcessState.ExitCode()
	} else {
		result.ExitCode = -1
	}
	return result, err
}

type safeBuffer struct {
	data []byte
}

const (
	maxCommandInput  = 1 << 20
	maxCommandOutput = 1 << 20
)

func (buffer *safeBuffer) Write(input []byte) (int, error) {
	remaining := maxCommandOutput - len(buffer.data)
	if remaining > 0 {
		if remaining > len(input) {
			remaining = len(input)
		}
		buffer.data = append(buffer.data, input[:remaining]...)
	}
	return len(input), nil
}

func (buffer *safeBuffer) Bytes() []byte {
	return append([]byte(nil), buffer.data...)
}
