package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	oldVerifyChecksum := verifyChecksum
	t.Cleanup(func() {
		repoURL = oldRepoURL
		downloadFile = oldDownloadFile
		runUpdateStep = oldRunUpdateStep
		osExecutable = oldOsExecutable
		verifyChecksum = oldVerifyChecksum
	})

	repoURL = "https://github.com/hirrrooo/mc-backup.git"
	osExecutable = func() (string, error) { return "/usr/local/bin/mc-backup", nil }

	downloadFile = func(url, dest string) error {
		calls = append(calls, "download:"+url+" "+dest)
		return nil
	}
	verifyChecksum = func(binaryPath, checksumURL string) error {
		calls = append(calls, "verify:"+binaryPath+" "+checksumURL)
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
		"verify:/usr/local/bin/mc-backup.new https://github.com/hirrrooo/mc-backup/releases/latest/download/mc-backup-linux-amd64.sha256",
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

	if !strings.Contains(stderr.String(), "update     Download and install the latest binary from GitHub") {
		t.Fatalf("usage output does not include update command:\n%s", stderr.String())
	}
}

func TestVerifyChecksumSuccess(t *testing.T) {
	body := []byte("not-a-real-binary-but-fine-for-hashing")
	binPath := filepath.Join(t.TempDir(), "mc-backup")
	if err := os.WriteFile(binPath, body, 0644); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	sum := sha256.Sum256(body)
	checksum := fmt.Sprintf("%x  mc-backup-linux-amd64\n", sum)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(checksum))
	}))
	defer srv.Close()

	if err := verifyChecksum(binPath, srv.URL); err != nil {
		t.Fatalf("verifyChecksum: %v", err)
	}
}

func TestVerifyChecksumMismatch(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "mc-backup")
	if err := os.WriteFile(binPath, []byte("real-bytes"), 0644); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("0000000000000000000000000000000000000000000000000000000000000000  mc-backup-linux-amd64\n"))
	}))
	defer srv.Close()

	err := verifyChecksum(binPath, srv.URL)
	if err == nil {
		t.Fatal("expected checksum mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch error, got: %v", err)
	}
}

func TestVerifyChecksumMissingSidecar(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "mc-backup")
	if err := os.WriteFile(binPath, []byte("real-bytes"), 0644); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	err := verifyChecksum(binPath, srv.URL)
	if err == nil {
		t.Fatal("expected error on missing checksum sidecar, got nil")
	}
	if !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("expected HTTP 404 error, got: %v", err)
	}
}
