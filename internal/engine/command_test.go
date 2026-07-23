package engine

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestWithCommandRunnerRestoresPreviousRunner(t *testing.T) {
	original := commandRunner
	called := false

	withCommandRunner(&fakeCommandRunner{fn: func(ctx context.Context, name string, args ...string) command {
		called = true
		return fakeCommand{}
	}}, func() {
		if commandRunner == original {
			t.Fatal("commandRunner was not replaced inside withCommandRunner")
		}
		if err := commandRunner.CommandContext(context.Background(), "true").Run(); err != nil {
			t.Fatalf("fake command failed: %v", err)
		}
	})

	if !called {
		t.Fatal("test runner was not called")
	}
	if commandRunner != original {
		t.Fatal("commandRunner was not restored")
	}
}

func TestRunSSHUsesCommandRunner(t *testing.T) {
	called := false

	withCommandRunner(commandRunnerFunc(func(ctx context.Context, name string, args ...string) command {
		called = true
		if name != "ssh" {
			t.Fatalf("name = %q, want ssh", name)
		}
		return fakeCommand{}
	}), func() {
		err := runSSH(context.Background(), NASConfig{
			SSHUser: "backup",
			SSHHost: "nas.example",
		}, "true")
		if err != nil {
			t.Fatalf("runSSH failed: %v", err)
		}
	})

	if !called {
		t.Fatal("runSSH did not use commandRunner")
	}
}

type fakeCommandRunner struct {
	fn func(ctx context.Context, name string, args ...string) command
}

func (r fakeCommandRunner) CommandContext(ctx context.Context, name string, args ...string) command {
	return r.fn(ctx, name, args...)
}

type fakeCommand struct{}

func (fakeCommand) Run() error { return nil }

func (fakeCommand) Output() ([]byte, error) { return nil, nil }

func (fakeCommand) CombinedOutput() ([]byte, error) { return nil, nil }

func (fakeCommand) SetStdout(io.Writer) {}

func (fakeCommand) SetStderr(io.Writer) {}

func (fakeCommand) SetEnv([]string) {}

func TestExecCommandSetEnv(t *testing.T) {
	ctx := context.Background()
	ec := execCommandRunner{}.CommandContext(ctx, "true").(execCommand)
	ec.SetEnv([]string{"TEST_ENV_VAR_1=value1"})
	if len(ec.cmd.Env) == 0 {
		t.Fatal("expected cmd.Env to be set")
	}
	hasPath := false
	hasVar1 := false
	for _, e := range ec.cmd.Env {
		if strings.HasPrefix(e, "PATH=") {
			hasPath = true
		}
		if e == "TEST_ENV_VAR_1=value1" {
			hasVar1 = true
		}
	}
	if !hasPath {
		t.Error("expected PATH from inherited os.Environ in cmd.Env")
	}
	if !hasVar1 {
		t.Error("expected TEST_ENV_VAR_1 in cmd.Env")
	}

	ec.SetEnv([]string{"TEST_ENV_VAR_2=value2"})
	hasVar2 := false
	for _, e := range ec.cmd.Env {
		if e == "TEST_ENV_VAR_2=value2" {
			hasVar2 = true
		}
	}
	if !hasVar1 || !hasVar2 {
		t.Error("expected both injected env vars to be present after second SetEnv call")
	}
}

func TestExecCommandSetEnvPreservesExplicitEmptyEnv(t *testing.T) {
	ctx := context.Background()
	ec := execCommandRunner{}.CommandContext(ctx, "true").(execCommand)
	ec.cmd.Env = []string{} // non-nil empty environment
	ec.SetEnv([]string{"FOO=bar"})

	if len(ec.cmd.Env) != 1 || ec.cmd.Env[0] != "FOO=bar" {
		t.Fatalf("expected ec.cmd.Env to be [FOO=bar], got: %v", ec.cmd.Env)
	}
}
