package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestUpdateCmdDownloadsAndInstalls(t *testing.T) {
	var calls []string
	oldRepoURL := repoURL
	oldDownloadFile := downloadFile
	oldRunUpdateStep := runUpdateStep
	oldOsExecutable := osExecutable
	t.Cleanup(func() {
		repoURL = oldRepoURL
		downloadFile = oldDownloadFile
		runUpdateStep = oldRunUpdateStep
		osExecutable = oldOsExecutable
	})

	repoURL = "https://github.com/hirrrooo/mc-backup.git"
	osExecutable = func() (string, error) { return "/usr/local/bin/mc-backup", nil }

	downloadFile = func(url, dest string) error {
		calls = append(calls, "download:"+url+" "+dest)
		return nil
	}
	runUpdateStep = func(dir, name string, command string, args ...string) error {
		calls = append(calls, name+":"+command+" "+strings.Join(args, " "))
		return nil
	}

	if err := runUpdate(); err != nil {
		t.Fatalf("runUpdate() error = %v", err)
	}

	want := []string{
		"download:https://github.com/hirrrooo/mc-backup/releases/latest/download/mc-backup-linux-amd64 /usr/local/bin/mc-backup.new",
		"Stopping mc-backup service:sudo systemctl stop mc-backup",
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
	t.Cleanup(func() {
		repoURL = oldRepoURL
	})

	repoURL = ""

	err := runUpdate()
	if err == nil {
		t.Fatal("expected error when repoURL is empty")
	}
	if !strings.Contains(err.Error(), "embedded repo URL") {
		t.Fatalf("expected embedded repo URL error, got: %v", err)
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
