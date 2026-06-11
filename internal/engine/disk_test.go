package engine

import (
	"os"
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

	size, err := dirSize(tmp)
	if err != nil {
		t.Fatalf("dirSize failed: %v", err)
	}
	if size < 10 {
		t.Errorf("dirSize too small: %d", size)
	}
}
