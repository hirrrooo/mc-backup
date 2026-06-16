package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestUpdateCmdRunsExpectedSteps(t *testing.T) {
	var calls []string
	oldFindRepoRoot := findRepoRoot
	oldRunUpdateStep := runUpdateStep
	t.Cleanup(func() {
		findRepoRoot = oldFindRepoRoot
		runUpdateStep = oldRunUpdateStep
	})

	findRepoRoot = func() (string, error) {
		calls = append(calls, "findRepoRoot")
		return "/repo", nil
	}
	runUpdateStep = func(dir, name string, command string, args ...string) error {
		calls = append(calls, dir+":"+name+":"+command+" "+strings.Join(args, " "))
		return nil
	}

	if err := runUpdate(); err != nil {
		t.Fatalf("runUpdate() error = %v", err)
	}

	want := []string{
		"findRepoRoot",
		"/repo:Pulling latest source:git pull --ff-only",
		"/repo:Installing mc-backup:sudo make install",
		"/repo:Restarting mc-backup service:sudo systemctl restart mc-backup",
		"/repo:mc-backup service status:systemctl status mc-backup --no-pager",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestPrintUsageIncludesUpdate(t *testing.T) {
	var stderr bytes.Buffer
	oldStderr := usageOutput
	t.Cleanup(func() { usageOutput = oldStderr })
	usageOutput = &stderr

	printUsage()

	if !strings.Contains(stderr.String(), "update     Pull latest source, install, and restart service") {
		t.Fatalf("usage output does not include update command:\n%s", stderr.String())
	}
}
