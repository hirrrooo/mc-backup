package engine

import (
	"context"
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
