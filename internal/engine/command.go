package engine

import (
	"context"
	"io"
	"os"
	"os/exec"
)

type command interface {
	Run() error
	Output() ([]byte, error)
	CombinedOutput() ([]byte, error)
	SetStdout(io.Writer)
	SetStderr(io.Writer)
	SetEnv([]string)
}

type commandRunnerInterface interface {
	CommandContext(ctx context.Context, name string, args ...string) command
}

type execCommandRunner struct{}

func (execCommandRunner) CommandContext(ctx context.Context, name string, args ...string) command {
	return execCommand{cmd: exec.CommandContext(ctx, name, args...)}
}

type execCommand struct {
	cmd *exec.Cmd
}

func (c execCommand) Run() error { return c.cmd.Run() }

func (c execCommand) Output() ([]byte, error) { return c.cmd.Output() }

func (c execCommand) CombinedOutput() ([]byte, error) { return c.cmd.CombinedOutput() }

func (c execCommand) SetStdout(w io.Writer) { c.cmd.Stdout = w }

func (c execCommand) SetStderr(w io.Writer) { c.cmd.Stderr = w }

func (c execCommand) SetEnv(env []string) {
	if c.cmd.Env == nil {
		c.cmd.Env = append(os.Environ(), env...)
	} else {
		c.cmd.Env = append(c.cmd.Env, env...)
	}
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
