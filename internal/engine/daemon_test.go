package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestProvisionServersConcurrent(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("[global]\n"), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	d := NewDaemon(cfgPath, &Config{Servers: map[string]ServerConfig{}})

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ns := []struct {
				Name   string
				Server ServerConfig
			}{{Name: fmt.Sprintf("srv%d", i), Server: ServerConfig{Enabled: true}}}
			d.provisionServers(d.ac.Load(), ns)
		}(i)
	}
	wg.Wait()

	d.autoMu.Lock()
	got := len(d.autoServers)
	d.autoMu.Unlock()
	if got != 16 {
		t.Fatalf("expected 16 auto servers, got %d", got)
	}
}

func TestServerMatches(t *testing.T) {
	cases := []struct {
		onlyServer, name string
		want             bool
	}{
		{"", "creative", true},
		{"", "", true},
		{"creative", "creative", true},
		{"Creative", "creative", true},
		{"CREATIVE", "creative", true},
		{"creative", "Creative", true},
		{"creative", "survival", false},
		{"creative", "creative-survival", false},
	}
	for _, c := range cases {
		if got := serverMatches(c.onlyServer, c.name); got != c.want {
			t.Errorf("serverMatches(%q, %q) = %v, want %v", c.onlyServer, c.name, got, c.want)
		}
	}
}
