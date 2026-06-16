package engine

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestDiskUsagePct(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(tmp+"/testfile", make([]byte, 1024), 0644)

	pct, err := diskUsagePct(tmp)
	if err != nil {
		t.Fatalf("diskUsagePct failed: %v", err)
	}
	if pct < 0 || pct > 100 {
		t.Errorf("diskUsagePct returned %f, expected 0-100", pct)
	}
}

func TestDirSize(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(tmp+"/a.txt", []byte("hello"), 0644)
	os.WriteFile(tmp+"/b.txt", []byte("world"), 0644)

	size, err := dirSize(tmp, nil)
	if err != nil {
		t.Fatalf("dirSize failed: %v", err)
	}
	if size < 10 {
		t.Errorf("dirSize too small: %d", size)
	}
}

func TestDirSizePassesExcludesToDu(t *testing.T) {
	var gotArgs []string
	withCommandRunner(commandRunnerFunc(func(ctx context.Context, name string, args ...string) command {
		gotArgs = append([]string{name}, args...)
		return fakeCommand{}
	}), func() {
		dirSize("/data", []string{"*.jar", "cache"})
	})

	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "--exclude=*.jar") || !strings.Contains(joined, "--exclude=cache") {
		t.Fatalf("du args missing excludes: %v", gotArgs)
	}
}
