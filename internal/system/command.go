// Package system provides injectable operating-system boundaries.
package system

import (
	"context"
	"os"
	"os/exec"
)

type Command struct {
	Name string
	Args []string
	Env  []string
	Dir  string
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
	process := exec.CommandContext(ctx, command.Name, command.Args...)
	process.Env = append(os.Environ(), command.Env...)
	process.Dir = command.Dir
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

const maxCommandOutput = 1 << 20

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
