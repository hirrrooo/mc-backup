package engine

import (
	"context"
	"os/exec"
)

type command interface {
	Run() error
	Output() ([]byte, error)
	CombinedOutput() ([]byte, error)
}

type commandRunnerInterface interface {
	CommandContext(ctx context.Context, name string, args ...string) command
}

type execCommandRunner struct{}

func (execCommandRunner) CommandContext(ctx context.Context, name string, args ...string) command {
	return exec.CommandContext(ctx, name, args...)
}

type commandRunnerFunc func(ctx context.Context, name string, args ...string) command

func (fn commandRunnerFunc) CommandContext(ctx context.Context, name string, args ...string) command {
	return fn(ctx, name, args...)
}

var commandRunner commandRunnerInterface = execCommandRunner{}

func withCommandRunner(runner commandRunnerInterface, fn func()) {
	previous := commandRunner
	commandRunner = runner
	defer func() { commandRunner = previous }()
	fn()
}
