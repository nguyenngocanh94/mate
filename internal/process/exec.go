package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// ExecRunner runs processes with os/exec. A non-zero exit is a result, not
// an error: callers read ExitCode. Failing to start the process (missing
// binary, bad directory) is an error. A cancelled or expired context returns
// ctx.Err(), per the Runner contract.
type ExecRunner struct{}

// Run implements Runner.
func (ExecRunner) Run(ctx context.Context, spec Spec) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if spec.Name == "" {
		return Result{}, errors.New("process: name is required")
	}
	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
	cmd.Dir = spec.Dir
	if spec.Env != nil {
		cmd.Env = spec.Env
	}
	if spec.Stdin != nil {
		cmd.Stdin = bytes.NewReader(spec.Stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Result{}, ctxErr
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return Result{ExitCode: exitErr.ExitCode(), Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, nil
		}
		return Result{}, fmt.Errorf("process %s: %w", spec.Name, err)
	}
	return Result{ExitCode: 0, Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, nil
}
