package main

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestUpdateCmdCachesRepoAndRunsSteps(t *testing.T) {
	var calls []string
	oldRepoURL := repoURL
	oldEnsureRepo := ensureRepo
	oldRunUpdateStep := runUpdateStep
	oldOsUserHomeDir := osUserHomeDir
	oldOsExecutable := osExecutable
	t.Cleanup(func() {
		repoURL = oldRepoURL
		ensureRepo = oldEnsureRepo
		runUpdateStep = oldRunUpdateStep
		osUserHomeDir = oldOsUserHomeDir
		osExecutable = oldOsExecutable
	})

	repoURL = "https://github.com/anomalyco/mc-backup"
	osUserHomeDir = func() (string, error) { return "/home/test", nil }
	osExecutable = func() (string, error) { return "/usr/local/bin/mc-backup", nil }

	ensureRepo = func(cacheDir, url string) (string, error) {
		calls = append(calls, "ensureRepo:"+cacheDir)
		return "/home/test/.cache/mc-backup/source", nil
	}
	runUpdateStep = func(dir, name string, command string, args ...string) error {
		calls = append(calls, name+":"+command+" "+strings.Join(args, " "))
		return nil
	}

	if err := runUpdate(); err != nil {
		t.Fatalf("runUpdate() error = %v", err)
	}

	want := []string{
		"ensureRepo:/home/test/.cache/mc-backup/source",
		"Stopping mc-backup service:sudo systemctl stop mc-backup",
		"Building mc-backup:go build -ldflags -X main.repoURL=https://github.com/anomalyco/mc-backup -o /usr/local/bin/mc-backup.new ./cmd/mc-backup",
		"Installing mc-backup:sudo mv /usr/local/bin/mc-backup.new /usr/local/bin/mc-backup",
		"Starting mc-backup service:sudo systemctl start mc-backup",
		"mc-backup service status:systemctl status mc-backup --no-pager",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls =\n%#v\nwant =\n%#v", calls, want)
	}
}

func TestUpdateCmdFallbackNoRepoURL(t *testing.T) {
	oldRepoURL := repoURL
	oldOsUserHomeDir := osUserHomeDir
	t.Cleanup(func() {
		repoURL = oldRepoURL
		osUserHomeDir = oldOsUserHomeDir
	})

	repoURL = ""
	osUserHomeDir = func() (string, error) { return "/home/test", nil }

	err := runUpdate()
	if err == nil {
		t.Fatal("expected error when repoURL is empty")
	}
	if !strings.Contains(err.Error(), "embedded repo URL") {
		t.Fatalf("expected embedded repo URL error, got: %v", err)
	}
}

func TestUpdateCmdEnsureRepoHomeDirError(t *testing.T) {
	oldRepoURL := repoURL
	oldOsUserHomeDir := osUserHomeDir
	t.Cleanup(func() {
		repoURL = oldRepoURL
		osUserHomeDir = oldOsUserHomeDir
	})

	repoURL = "https://github.com/anomalyco/mc-backup"
	osUserHomeDir = func() (string, error) { return "", errors.New("no home") }

	err := runUpdate()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "home") {
		t.Fatalf("expected home dir error, got: %v", err)
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
