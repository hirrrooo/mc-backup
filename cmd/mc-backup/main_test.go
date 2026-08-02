package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"mc-backup/internal/engine"
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

	if err := runUpdate(io.Discard, io.Discard); err != nil {
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

	err := runUpdate(io.Discard, io.Discard)
	if err == nil {
		t.Fatal("expected error when repoURL is empty")
	}
	if !strings.Contains(err.Error(), "embedded repo URL") {
		t.Fatalf("expected embedded repo URL error, got: %v", err)
	}
}

func TestPrintUsageDocumentsConfigKeysAndUpdate(t *testing.T) {
	var stderr bytes.Buffer
	oldStderr := usageOutput
	t.Cleanup(func() { usageOutput = oldStderr })
	usageOutput = &stderr

	printUsage()

	if !strings.Contains(stderr.String(), "update     Download and install the latest binary from GitHub") {
		t.Fatalf("usage output does not include update command:\n%s", stderr.String())
	}
	if strings.Contains(stderr.String(), "archive") {
		t.Fatalf("usage output contains stale archive terminology:\n%s", stderr.String())
	}
	for _, key := range []string{"target", "local.dest_root"} {
		if !strings.Contains(stderr.String(), key) {
			t.Fatalf("usage output does not document %q:\n%s", key, stderr.String())
		}
	}
}

func TestVerifyChecksumSuccess(t *testing.T) {
	body := []byte("not-a-real-binary-but-fine-for-hashing")
	binPath := filepath.Join(t.TempDir(), "mc-backup")
	if err := os.WriteFile(binPath, body, 0600); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	sum := sha256.Sum256(body)
	checksum := fmt.Sprintf("%x  mc-backup-linux-amd64\n", sum)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(checksum))
	}))
	defer srv.Close()

	if err := verifyChecksum(binPath, srv.URL); err != nil {
		t.Fatalf("verifyChecksum: %v", err)
	}
}

func TestVerifyChecksumMismatch(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "mc-backup")
	if err := os.WriteFile(binPath, []byte("real-bytes"), 0600); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("0000000000000000000000000000000000000000000000000000000000000000  mc-backup-linux-amd64\n"))
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
	if err := os.WriteFile(binPath, []byte("real-bytes"), 0600); err != nil {
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

func TestCLIMutatingCommandsBearerToken(t *testing.T) {
	var receivedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	cfgContent := fmt.Sprintf(`
[global]
listen_addr = "%s"
api_token = "cli-secret-token"
`, srv.Listener.Addr().String())
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Test postCmd ("scan")
	receivedAuth = ""
	if code := postCmd("scan", []string{"--config", cfgPath}, io.Discard, io.Discard); code != 0 {
		t.Errorf("scan postCmd exit code = %d, want 0", code)
	}
	if receivedAuth != "Bearer cli-secret-token" {
		t.Errorf("scan CLI command auth header = %q, want %q", receivedAuth, "Bearer cli-secret-token")
	}

	// Test backupCmd
	receivedAuth = ""
	var receivedQuery string
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		receivedQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv2.Close()

	cfgPath2 := filepath.Join(tmp, "config2.toml")
	cfgContent2 := fmt.Sprintf(`
[global]
listen_addr = "%s"
api_token = "cli-secret-token"
`, srv2.Listener.Addr().String())
	if err := os.WriteFile(cfgPath2, []byte(cfgContent2), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if code := backupCmd([]string{"--config", cfgPath2, "--offline", "creative"}, io.Discard, io.Discard); code != 0 {
		t.Errorf("backupCmd exit code = %d, want 0", code)
	}
	if receivedAuth != "Bearer cli-secret-token" {
		t.Errorf("backup CLI command auth header = %q, want %q", receivedAuth, "Bearer cli-secret-token")
	}
	if !strings.Contains(receivedQuery, "offline=true") || !strings.Contains(receivedQuery, "server=creative") {
		t.Errorf("backup CLI query = %q, want offline=true and server=creative", receivedQuery)
	}
}

type chunkReader struct {
	data      []byte
	chunkSize int
	pos       int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.pos >= len(c.data) {
		return 0, io.EOF
	}
	n := c.chunkSize
	if len(c.data)-c.pos < n {
		n = len(c.data) - c.pos
	}
	if len(p) < n {
		n = len(p)
	}
	copy(p[:n], c.data[c.pos:c.pos+n])
	c.pos += n
	return n, nil
}

type errTestReader struct{}

func (e *errTestReader) Read(p []byte) (int, error) {
	return 0, fmt.Errorf("underlying read error")
}

func TestReadResponseBody(t *testing.T) {
	t.Run("chunked short reads combine full response > 64 bytes", func(t *testing.T) {
		data := bytes.Repeat([]byte("a"), 100) // 100 bytes > 64
		cr := &chunkReader{data: data, chunkSize: 10}
		body, err := readResponseBody(cr)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !bytes.Equal(body, data) {
			t.Fatalf("readResponseBody len = %d, want %d", len(body), len(data))
		}
	})

	t.Run("exactly cap succeeds", func(t *testing.T) {
		data := make([]byte, maxResponseBodyBytes)
		cr := &chunkReader{data: data, chunkSize: 64 * 1024}
		body, err := readResponseBody(cr)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(body) != maxResponseBodyBytes {
			t.Fatalf("readResponseBody len = %d, want %d", len(body), maxResponseBodyBytes)
		}
	})

	t.Run("cap plus one fails", func(t *testing.T) {
		data := make([]byte, maxResponseBodyBytes+1)
		cr := &chunkReader{data: data, chunkSize: 64 * 1024}
		_, err := readResponseBody(cr)
		if err == nil {
			t.Fatal("expected error when body exceeds cap, got nil")
		}
		if !strings.Contains(err.Error(), "exceed") {
			t.Fatalf("expected error containing 'exceed', got %v", err)
		}
	})

	t.Run("underlying read error propagates", func(t *testing.T) {
		_, err := readResponseBody(&errTestReader{})
		if err == nil {
			t.Fatal("expected error from underlying reader, got nil")
		}
		if !strings.Contains(err.Error(), "underlying read error") {
			t.Fatalf("expected 'underlying read error', got %v", err)
		}
	})
}

func TestVerifyChecksumEmptySidecar(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "mc-backup")
	if err := os.WriteFile(binPath, []byte("real-bytes"), 0600); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("   \n\t  "))
	}))
	defer srv.Close()

	err := verifyChecksum(binPath, srv.URL)
	if err == nil {
		t.Fatal("expected error on empty checksum sidecar, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected 'empty' error, got: %v", err)
	}
}

func TestVerifyChecksumUppercaseHexSuccess(t *testing.T) {
	body := []byte("binary-contents-for-uppercase-hash")
	binPath := filepath.Join(t.TempDir(), "mc-backup")
	if err := os.WriteFile(binPath, body, 0600); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	sum := sha256.Sum256(body)
	uppercaseChecksum := fmt.Sprintf("%X  mc-backup-linux-amd64\n", sum)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(uppercaseChecksum))
	}))
	defer srv.Close()

	if err := verifyChecksum(binPath, srv.URL); err != nil {
		t.Fatalf("verifyChecksum with uppercase hex failed: %v", err)
	}
}

func TestVerifyChecksumMissingBinaryFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("a" + strings.Repeat("0", 63) + "  mc-backup-linux-amd64\n"))
	}))
	defer srv.Close()

	missingPath := filepath.Join(t.TempDir(), "does-not-exist")
	err := verifyChecksum(missingPath, srv.URL)
	if err == nil {
		t.Fatal("expected error opening missing binary file, got nil")
	}
	if !strings.Contains(err.Error(), "open") {
		t.Fatalf("expected open error, got: %v", err)
	}
}

func TestDeriveReleaseURL(t *testing.T) {
	url := deriveReleaseURL("https://github.com/hirrrooo/mc-backup.git")
	want := "https://github.com/hirrrooo/mc-backup/releases/latest/download/mc-backup-linux-amd64"
	if url != want {
		t.Errorf("deriveReleaseURL = %q, want %q", url, want)
	}
}

func TestFindConfig(t *testing.T) {
	path := findConfig()
	if path == "" {
		t.Fatal("findConfig returned empty string")
	}
}
func TestRunCLIBasicAndUnknown(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := runCLI([]string{}, &out, &errOut); code != 1 {
		t.Errorf("runCLI() with no args code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "mc-backup") {
		t.Errorf("no args stderr = %q, want usage", errOut.String())
	}

	out.Reset()
	errOut.Reset()
	if code := runCLI([]string{"unknowncmd"}, &out, &errOut); code != 1 {
		t.Errorf("runCLI() with unknowncmd code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "unknown command: unknowncmd") {
		t.Errorf("unknowncmd stderr = %q", errOut.String())
	}

	for _, vFlag := range []string{"version", "--version", "-v"} {
		out.Reset()
		errOut.Reset()
		if code := runCLI([]string{vFlag}, &out, &errOut); code != 0 {
			t.Errorf("runCLI() %s code = %d, want 0", vFlag, code)
		}
		if !strings.Contains(out.String(), "mc-backup") {
			t.Errorf("%s stdout = %q", vFlag, out.String())
		}
	}
}

func TestBackupCmdBranches(t *testing.T) {
	var out, errOut bytes.Buffer

	// Invalid flag
	if code := backupCmd([]string{"--invalidflag"}, &out, &errOut); code != 1 {
		t.Errorf("backupCmd invalid flag code = %d, want 1", code)
	}

	// Config load error
	out.Reset()
	errOut.Reset()
	if code := backupCmd([]string{"--config", "/nonexistent/path/config.toml"}, &out, &errOut); code != 1 {
		t.Errorf("backupCmd bad config code = %d, want 1", code)
	}

	// Server setup with HTTP 500 & 401 & 200 over-size
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("server") {
		case "err500":
			w.WriteHeader(http.StatusInternalServerError)
		case "err401":
			w.WriteHeader(http.StatusUnauthorized)
		case "errbig":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(bytes.Repeat([]byte("a"), maxResponseBodyBytes+10))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("backup started"))
		}
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	_ = os.WriteFile(cfgPath, []byte(fmt.Sprintf("[global]\nlisten_addr = %q\n", srv.Listener.Addr().String())), 0600)

	// Test 500
	out.Reset()
	errOut.Reset()
	if code := backupCmd([]string{"--config", cfgPath, "err500"}, &out, &errOut); code != 1 {
		t.Errorf("backupCmd 500 code = %d, want 1", code)
	}

	// Test 401
	out.Reset()
	errOut.Reset()
	if code := backupCmd([]string{"--config", cfgPath, "err401"}, &out, &errOut); code != 1 {
		t.Errorf("backupCmd 401 code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "unauthorized") {
		t.Errorf("backupCmd 401 errOut = %q, want unauthorized", errOut.String())
	}

	// Test oversize body
	out.Reset()
	errOut.Reset()
	if code := backupCmd([]string{"--config", cfgPath, "errbig"}, &out, &errOut); code != 1 {
		t.Errorf("backupCmd errbig code = %d, want 1", code)
	}

	// Test cwd match when server arg is empty
	cfgPathWithServer := filepath.Join(tmpDir, "cfg_server.toml")
	_ = os.WriteFile(cfgPathWithServer, []byte(fmt.Sprintf("[global]\nlisten_addr = %q\n[server.testdir]\npath = %q\n", srv.Listener.Addr().String(), tmpDir)), 0600)

	workDir := filepath.Join(tmpDir, "testdir")
	_ = os.MkdirAll(workDir, 0755)
	oldCwd, _ := os.Getwd()
	_ = os.Chdir(workDir)
	defer func() { _ = os.Chdir(oldCwd) }()

	out.Reset()
	errOut.Reset()
	if code := backupCmd([]string{"--config", cfgPathWithServer}, &out, &errOut); code != 0 {
		t.Errorf("backupCmd cwd server infer code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "backup started") {
		t.Errorf("backupCmd stdout = %q, want backup started", out.String())
	}
}

func TestPostCmdBranches(t *testing.T) {
	var out, errOut bytes.Buffer

	if code := postCmd("scan", []string{"--invalidflag"}, &out, &errOut); code != 1 {
		t.Errorf("postCmd invalid flag code = %d, want 1", code)
	}

	if code := postCmd("scan", []string{"--config", "/nonexistent/cfg.toml"}, &out, &errOut); code != 1 {
		t.Errorf("postCmd bad config code = %d, want 1", code)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("status") {
		case "401":
			w.WriteHeader(http.StatusUnauthorized)
		case "405":
			w.WriteHeader(http.StatusMethodNotAllowed)
		case "500":
			w.WriteHeader(http.StatusInternalServerError)
		case "big":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(bytes.Repeat([]byte("x"), maxResponseBodyBytes+5))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("scanned"))
		}
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	_ = os.WriteFile(cfgPath, []byte(fmt.Sprintf("[global]\nlisten_addr = %q\n", srv.Listener.Addr().String())), 0600)

	for status, wantErr := range map[string]string{
		"401": "unauthorized",
		"405": "not responding to POST",
		"500": "unexpected status 500",
		"big": "response body exceeded limit",
	} {
		out.Reset()
		errOut.Reset()
		// Use endpoint query string to pass status to test server
		endpoint := "scan?status=" + status
		if code := postCmd(endpoint, []string{"--config", cfgPath}, &out, &errOut); code != 1 {
			t.Errorf("postCmd %s code = %d, want 1", status, code)
		}
		if !strings.Contains(errOut.String(), wantErr) {
			t.Errorf("postCmd %s errOut = %q, want containing %q", status, errOut.String(), wantErr)
		}
	}
}

func TestUpdateInvariants(t *testing.T) {
	var out, errOut bytes.Buffer

	oldRepoURL := repoURL
	oldOsExec := osExecutable
	oldDownload := downloadFile
	oldVerify := verifyChecksum
	oldStep := runUpdateStep
	t.Cleanup(func() {
		repoURL = oldRepoURL
		osExecutable = oldOsExec
		downloadFile = oldDownload
		verifyChecksum = oldVerify
		runUpdateStep = oldStep
	})

	repoURL = "https://github.com/hirrrooo/mc-backup.git"
	tmpDir := t.TempDir()
	targetBin := filepath.Join(tmpDir, "mc-backup")
	tmpBin := targetBin + ".new"
	_ = os.WriteFile(targetBin, []byte("old-bin"), 0755)

	osExecutable = func() (string, error) { return targetBin, nil }

	// Checksum failure MUST remove tmpBin (.new file)
	downloadFile = func(url, dest string) error {
		return os.WriteFile(dest, []byte("downloaded-new-bin"), 0755)
	}
	verifyChecksum = func(binaryPath, checksumURL string) error {
		return fmt.Errorf("checksum mismatch test")
	}

	err := runUpdate(&out, &errOut)
	if err == nil {
		t.Fatal("expected runUpdate to fail on checksum mismatch")
	}
	if _, statErr := os.Stat(tmpBin); !os.IsNotExist(statErr) {
		t.Fatalf("expected tmpBin %s to be removed on checksum failure, but it still exists", tmpBin)
	}

	// Step failure MUST stop before install step (step 2)
	verifyChecksum = func(binaryPath, checksumURL string) error { return nil }

	var attemptedSteps []string
	runUpdateStep = func(dir, name string, command string, args ...string) error {
		attemptedSteps = append(attemptedSteps, name)
		if name == "Stopping mc-backup service" {
			return fmt.Errorf("failed to stop service")
		}
		return nil
	}

	err = runUpdate(&out, &errOut)
	if err == nil {
		t.Fatal("expected runUpdate to fail when step 1 fails")
	}
	if len(attemptedSteps) != 1 || attemptedSteps[0] != "Stopping mc-backup service" {
		t.Fatalf("attempted steps = %v, expected ONLY step 1 before stopping", attemptedSteps)
	}

	// Test osExecutable error
	osExecutable = func() (string, error) { return "", fmt.Errorf("osExecutable fail") }
	if err := runUpdate(&out, &errOut); err == nil {
		t.Fatal("expected error on osExecutable failure")
	}

	// Test download error
	osExecutable = func() (string, error) { return targetBin, nil }
	downloadFile = func(url, dest string) error { return fmt.Errorf("dl error") }
	if err := runUpdate(&out, &errOut); err == nil {
		t.Fatal("expected error on downloadFile failure")
	}

	// Test updateCmd wrapper
	if code := updateCmd(&out, &errOut); code != 1 {
		t.Errorf("updateCmd error code = %d, want 1", code)
	}
}

func TestDownloadFileAndVerifyChecksumBranches(t *testing.T) {
	tmpDir := t.TempDir()

	// downloadFile HTTP 404
	srv404 := httptest.NewServer(http.NotFoundHandler())
	defer srv404.Close()

	destPath := filepath.Join(tmpDir, "testdl")
	if err := downloadFile(srv404.URL, destPath); err == nil {
		t.Fatal("expected downloadFile error on 404")
	}

	// downloadFile success
	srv200 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("dl content"))
	}))
	defer srv200.Close()

	if err := downloadFile(srv200.URL, destPath); err != nil {
		t.Fatalf("downloadFile failed: %v", err)
	}

	// verifyChecksum 404
	if err := verifyChecksum(destPath, srv404.URL); err == nil {
		t.Fatal("expected verifyChecksum error on 404")
	}
}

func TestRunCmdAndStatusCmdAndConfigCmd(t *testing.T) {
	var out, errOut bytes.Buffer

	// runCmd invalid flags & bad config
	if code := runCmd([]string{"--invalid"}, &out, &errOut); code != 1 {
		t.Errorf("runCmd invalid flag code = %d, want 1", code)
	}
	if code := runCmd([]string{"--config", "/bad/path.toml"}, &out, &errOut); code != 1 {
		t.Errorf("runCmd bad config code = %d, want 1", code)
	}

	// statusCmd invalid flags & bad config & unreachable status server
	out.Reset()
	errOut.Reset()
	if code := statusCmd([]string{"--invalid"}, &out, &errOut); code != 1 {
		t.Errorf("statusCmd invalid flag code = %d, want 1", code)
	}
	out.Reset()
	errOut.Reset()
	if code := statusCmd([]string{"--config", "/bad/path.toml"}, &out, &errOut); code != 1 {
		t.Errorf("statusCmd bad config code = %d, want 1", code)
	}

	// configCmd paths
	out.Reset()
	errOut.Reset()
	if code := configCmd([]string{}, &out, &errOut); code != 1 {
		t.Errorf("configCmd no args code = %d, want 1", code)
	}
	out.Reset()
	errOut.Reset()
	if code := configCmd([]string{"get"}, &out, &errOut); code != 1 {
		t.Errorf("configCmd get no key code = %d, want 1", code)
	}
	out.Reset()
	errOut.Reset()
	if code := configCmd([]string{"--config", "/bad/path.toml", "get", "global.listen_addr"}, &out, &errOut); code != 1 {
		t.Errorf("configCmd get bad config code = %d, want 1", code)
	}
	out.Reset()
	errOut.Reset()
	if code := configCmd([]string{"set"}, &out, &errOut); code != 1 {
		t.Errorf("configCmd set no args code = %d, want 1", code)
	}
	out.Reset()
	errOut.Reset()
	if code := configCmd([]string{"set", "key"}, &out, &errOut); code != 1 {
		t.Errorf("configCmd set no value code = %d, want 1", code)
	}
	out.Reset()
	errOut.Reset()
	if code := configCmd([]string{"unknownaction"}, &out, &errOut); code != 1 {
		t.Errorf("configCmd unknown action code = %d, want 1", code)
	}

	// Test config get & set success
	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "config.toml")
	_ = os.WriteFile(cfgFile, []byte("[global]\nlisten_addr = \"127.0.0.1:8080\"\n"), 0600)

	out.Reset()
	errOut.Reset()
	if code := configCmd([]string{"--config", cfgFile, "get", "global.listen_addr"}, &out, &errOut); code != 0 {
		t.Errorf("configCmd get success code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "127.0.0.1:8080") {
		t.Errorf("configCmd get out = %q", out.String())
	}

	out.Reset()
	errOut.Reset()
	if code := configCmd([]string{"--config", cfgFile, "set", "global.listen_addr", "127.0.0.1:9090"}, &out, &errOut); code != 0 {
		t.Errorf("configCmd set success code = %d, want 0", code)
	}

	out.Reset()
	errOut.Reset()
	if code := configCmd([]string{"--config", cfgFile, "get", "global.listen_addr"}, &out, &errOut); code != 0 {
		t.Errorf("configCmd get updated code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "127.0.0.1:9090") {
		t.Errorf("configCmd get out = %q, want 127.0.0.1:9090", out.String())
	}
}

func TestDefaultRunUpdateStepExecution(t *testing.T) {
	if err := runUpdateStep("", "Test Echo", "echo", "hello"); err != nil {
		t.Fatalf("default runUpdateStep failed: %v", err)
	}
}

func TestFindConfigWithHomeDir(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	cfgDir := filepath.Join(tmpHome, ".config", "mc-backup")
	_ = os.MkdirAll(cfgDir, 0755)
	cfgFile := filepath.Join(cfgDir, "config.toml")
	_ = os.WriteFile(cfgFile, []byte("# test"), 0600)

	got := findConfig()
	if got != cfgFile {
		t.Errorf("findConfig = %q, want %q", got, cfgFile)
	}
}
func TestMainFunc(t *testing.T) {
	oldExit := osExit
	var exitCode int
	osExit = func(code int) { exitCode = code }
	t.Cleanup(func() { osExit = oldExit })

	oldArgs := os.Args
	os.Args = []string{"mc-backup", "version"}
	t.Cleanup(func() { os.Args = oldArgs })

	main()
	if exitCode != 0 {
		t.Errorf("main() exitCode = %d, want 0", exitCode)
	}
}

func TestRunCLIAllSubcommands(t *testing.T) {
	tmpDir := t.TempDir()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	closedAddr := l.Addr().String()
	_ = l.Close()

	cfgPath := filepath.Join(tmpDir, "config.toml")
	_ = os.WriteFile(cfgPath, []byte(fmt.Sprintf("[global]\nlisten_addr = %q\n", closedAddr)), 0600)
	oldDaemon := runDaemon
	runDaemon = func(path string, cfg *engine.Config, debug bool) error { return nil }
	t.Cleanup(func() { runDaemon = oldDaemon })

	var out, errOut bytes.Buffer

	// run
	out.Reset()
	errOut.Reset()
	if code := runCLI([]string{"run", "--config", cfgPath}, &out, &errOut); code != 0 {
		t.Errorf("runCLI run code = %d, want 0", code)
	}

	// status (daemon not running -> 1)
	out.Reset()
	errOut.Reset()
	if code := runCLI([]string{"status", "--config", cfgPath}, &out, &errOut); code != 1 {
		t.Errorf("runCLI status code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "status error") {
		t.Errorf("runCLI status errOut = %q, want status error", errOut.String())
	}

	// config
	out.Reset()
	errOut.Reset()
	if code := runCLI([]string{"config", "--config", cfgPath, "get", "global.listen_addr"}, &out, &errOut); code != 0 {
		t.Errorf("runCLI config get code = %d, want 0", code)
	}

	// backup (daemon not running -> 1)
	out.Reset()
	errOut.Reset()
	if code := runCLI([]string{"backup", "--config", cfgPath, "survival"}, &out, &errOut); code != 1 {
		t.Errorf("runCLI backup code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "backup failed") {
		t.Errorf("runCLI backup errOut = %q, want backup failed", errOut.String())
	}

	// scan (daemon not running -> 1)
	out.Reset()
	errOut.Reset()
	if code := runCLI([]string{"scan", "--config", cfgPath}, &out, &errOut); code != 1 {
		t.Errorf("runCLI scan code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "scan failed") {
		t.Errorf("runCLI scan errOut = %q, want scan failed", errOut.String())
	}

	// cancel (daemon not running -> 1)
	out.Reset()
	errOut.Reset()
	if code := runCLI([]string{"cancel", "--config", cfgPath}, &out, &errOut); code != 1 {
		t.Errorf("runCLI cancel code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "cancel failed") {
		t.Errorf("runCLI cancel errOut = %q, want cancel failed", errOut.String())
	}
	// update fallback
	oldRepoURL := repoURL
	repoURL = ""
	t.Cleanup(func() { repoURL = oldRepoURL })
	out.Reset()
	errOut.Reset()
	if code := runCLI([]string{"update"}, &out, &errOut); code != 1 {
		t.Errorf("runCLI update code = %d, want 1", code)
	}
}

func TestRunCmdDaemonSeam(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	_ = os.WriteFile(cfgPath, []byte("[global]\nlisten_addr = \"127.0.0.1:8080\"\n"), 0600)

	oldDaemon := runDaemon
	t.Cleanup(func() { runDaemon = oldDaemon })

	var out, errOut bytes.Buffer

	runDaemon = func(path string, cfg *engine.Config, debug bool) error {
		return fmt.Errorf("mock daemon failure")
	}
	if code := runCmd([]string{"--config", cfgPath}, &out, &errOut); code != 1 {
		t.Errorf("runCmd daemon error code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "mock daemon failure") {
		t.Errorf("runCmd errOut = %q, want mock daemon failure", errOut.String())
	}
}

func TestStatusCmdSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	_ = os.WriteFile(cfgPath, []byte(fmt.Sprintf("[global]\nlisten_addr = %q\n", srv.Listener.Addr().String())), 0600)

	var out, errOut bytes.Buffer
	if code := statusCmd([]string{"--config", cfgPath}, &out, &errOut); code != 0 {
		t.Errorf("statusCmd success code = %d, want 0", code)
	}
}

func TestDownloadFileErrorPaths(t *testing.T) {
	tmpDir := t.TempDir()

	// Get error
	if err := downloadFile("http://invalid.localhost:99999", filepath.Join(tmpDir, "a")); err == nil {
		t.Fatal("expected Get error for invalid URL")
	}

	// Create error
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	if err := downloadFile(srv.URL, filepath.Join(tmpDir, "nonexistent_dir", "file")); err == nil {
		t.Fatal("expected Create error for missing dir")
	}
}

func TestVerifyChecksumErrorPaths(t *testing.T) {
	tmpDir := t.TempDir()
	binFile := filepath.Join(tmpDir, "bin")
	_ = os.WriteFile(binFile, []byte("data"), 0600)

	// Get error
	if err := verifyChecksum(binFile, "http://invalid.localhost:99999"); err == nil {
		t.Fatal("expected Get error for invalid URL")
	}
}

func TestFindConfigFallbackPaths(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// HOME exists but .config/mc-backup/config.toml does not exist
	// Test fallback to defaultConfigPaths
	oldPaths := defaultConfigPaths
	tmpCfg := filepath.Join(tmpHome, "etc_config.toml")
	_ = os.WriteFile(tmpCfg, []byte("# test"), 0600)
	defaultConfigPaths = []string{tmpCfg}
	t.Cleanup(func() { defaultConfigPaths = oldPaths })

	got := findConfig()
	if got != tmpCfg {
		t.Errorf("findConfig = %q, want %q", got, tmpCfg)
	}
}
func TestUpdateCmdSuccess(t *testing.T) {
	oldRepoURL := repoURL
	oldOsExec := osExecutable
	oldDownload := downloadFile
	oldVerify := verifyChecksum
	oldStep := runUpdateStep
	t.Cleanup(func() {
		repoURL = oldRepoURL
		osExecutable = oldOsExec
		downloadFile = oldDownload
		verifyChecksum = oldVerify
		runUpdateStep = oldStep
	})

	repoURL = "https://github.com/hirrrooo/mc-backup.git"
	tmpDir := t.TempDir()
	targetBin := filepath.Join(tmpDir, "mc-backup")
	osExecutable = func() (string, error) { return targetBin, nil }
	downloadFile = func(url, dest string) error { return nil }
	verifyChecksum = func(binaryPath, checksumURL string) error { return nil }
	runUpdateStep = func(dir, name string, command string, args ...string) error { return nil }

	var out, errOut bytes.Buffer
	if code := updateCmd(&out, &errOut); code != 0 {
		t.Errorf("updateCmd success code = %d, want 0", code)
	}
}

func TestConfigCmdSetAndInvalidFlagError(t *testing.T) {
	var out, errOut bytes.Buffer
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	_ = os.WriteFile(cfgPath, []byte("[global]\nlisten_addr = \"127.0.0.1:8080\"\n"), 0600)

	if code := configCmd([]string{"--invalidflag"}, &out, &errOut); code != 1 {
		t.Errorf("configCmd invalid flag code = %d, want 1", code)
	}

	out.Reset()
	errOut.Reset()
	if code := configCmd([]string{"--config", cfgPath, "set", "invalid.key.path", "value"}, &out, &errOut); code != 1 {
		t.Errorf("configCmd set invalid key code = %d, want 1", code)
	}
}

func TestDownloadAndChecksumErrorPathsExhaustive(t *testing.T) {
	tmpDir := t.TempDir()

	// downloadFile Copy error (stream error)
	srvCopyErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("short"))
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
		}
	}))
	defer srvCopyErr.Close()

	dest := filepath.Join(tmpDir, "dest_copy_err")
	if err := downloadFile(srvCopyErr.URL, dest); err == nil {
		t.Fatal("expected downloadFile error on broken stream")
	}

	// verifyChecksum read body error
	srvBodyErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("short"))
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
		}
	}))
	defer srvBodyErr.Close()

	binPath := filepath.Join(tmpDir, "bin_file")
	_ = os.WriteFile(binPath, []byte("data"), 0600)
	if err := verifyChecksum(binPath, srvBodyErr.URL); err == nil {
		t.Fatal("expected verifyChecksum error on broken stream")
	}
}

func TestHTTPNewRequestErrorPaths(t *testing.T) {
	oldReq := newHTTPRequest
	newHTTPRequest = func(method, url string, body io.Reader) (*http.Request, error) {
		return nil, fmt.Errorf("mock request error")
	}
	t.Cleanup(func() { newHTTPRequest = oldReq })

	var out, errOut bytes.Buffer
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	_ = os.WriteFile(cfgPath, []byte("[global]\nlisten_addr = \"127.0.0.1:8080\"\n"), 0600)

	if code := backupCmd([]string{"--config", cfgPath, "survival"}, &out, &errOut); code != 1 {
		t.Errorf("backupCmd newHTTPRequest error code = %d, want 1", code)
	}
	out.Reset()
	errOut.Reset()
	if code := postCmd("scan", []string{"--config", cfgPath}, &out, &errOut); code != 1 {
		t.Errorf("postCmd newHTTPRequest error code = %d, want 1", code)
	}
}

func TestBackupCmdInferServerCwdNotFound(t *testing.T) {
	var receivedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	nonMatchingDir := filepath.Join(tmpDir, "unmatched_server_dir")
	_ = os.MkdirAll(nonMatchingDir, 0755)

	oldCwd, _ := os.Getwd()
	_ = os.Chdir(nonMatchingDir)
	defer func() { _ = os.Chdir(oldCwd) }()
	cfgPath := filepath.Join(nonMatchingDir, "config.toml")
	_ = os.WriteFile(cfgPath, []byte(fmt.Sprintf("[global]\nlisten_addr = %q\n[server.other]\npath = %q\n", srv.Listener.Addr().String(), tmpDir)), 0600)
	var out, errOut bytes.Buffer
	if code := backupCmd([]string{"--config", cfgPath}, &out, &errOut); code != 0 {
		t.Errorf("backupCmd code = %d, want 0", code)
	}
	if strings.Contains(receivedQuery, "server=") {
		t.Errorf("expected no server query param when CWD is not in cfg.Servers, got %q", receivedQuery)
	}
}
func TestVerifyChecksumCopyError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  mc-backup-linux-amd64\n"))
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	err := verifyChecksum(tmpDir, srv.URL)
	if err == nil {
		t.Fatal("expected verifyChecksum error on directory path")
	}
}
