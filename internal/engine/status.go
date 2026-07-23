package engine

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"text/tabwriter"

	"log/slog"
)

type JobInfo struct {
	ServerName string `json:"server_name"`
	Snapshot   string `json:"snapshot"`
	State      string `json:"state"`
	BytesMoved int64  `json:"bytes_moved"`
	TotalSize  int64  `json:"total_size"`
}

type JobTracker struct {
	mu   sync.RWMutex
	jobs map[string]*JobInfo
}

func NewJobTracker() *JobTracker {
	return &JobTracker{jobs: make(map[string]*JobInfo)}
}

func (jt *JobTracker) Add(key string, info *JobInfo) {
	jt.mu.Lock()
	jt.jobs[key] = info
	jt.mu.Unlock()
}

func (jt *JobTracker) Remove(key string) {
	jt.mu.Lock()
	delete(jt.jobs, key)
	jt.mu.Unlock()
}

func (jt *JobTracker) Snapshot() map[string]*JobInfo {
	jt.mu.RLock()
	defer jt.mu.RUnlock()
	snap := make(map[string]*JobInfo, len(jt.jobs))
	for k, v := range jt.jobs {
		cp := *v
		snap[k] = &cp
	}
	return snap
}

type StatusCallbacks struct {
	OnCancel func()
	OnScan   func()
	OnBackup func(server string, offline bool)
}

func checkBearerToken(r *http.Request, tokenSupplier func() string) bool {
	var token string
	if tokenSupplier != nil {
		token = tokenSupplier()
	}
	if token == "" {
		return true
	}
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	got := strings.TrimPrefix(auth, prefix)
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

func newStatusMux(jt *JobTracker, callbacks StatusCallbacks, tokenSupplier func() string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jt.Snapshot())
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/cancel", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !checkBearerToken(r, tokenSupplier) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		callbacks.OnCancel()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("canceled"))
	})
	mux.HandleFunc("/scan", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !checkBearerToken(r, tokenSupplier) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		callbacks.OnScan()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("scan triggered"))
	})
	mux.HandleFunc("/backup", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !checkBearerToken(r, tokenSupplier) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		server := r.URL.Query().Get("server")
		offline := r.URL.Query().Get("offline") == "true"
		callbacks.OnBackup(server, offline)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("backup triggered"))
	})
	return mux
}

func startStatusServer(addr string, tokenSupplier func() string, jt *JobTracker, callbacks StatusCallbacks) {
	handler := newStatusMux(jt, callbacks, tokenSupplier)
	go func() {
		slog.Info("status API listening", "addr", addr)
		if err := http.ListenAndServe(addr, handler); err != nil {
			slog.Error("status API server error", "error", err)
		}
	}()
}

func PrintDashboard(addr string) error {
	resp, err := http.Get(fmt.Sprintf("http://%s/status", addr))
	if err != nil {
		return fmt.Errorf("cannot connect to daemon at %s: %w", addr, err)
	}
	defer resp.Body.Close()

	var jobs map[string]*JobInfo
	if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	fmt.Println()
	fmt.Println("[Minecraft Backup & Archive - Live Status]")
	fmt.Println()

	if len(jobs) == 0 {
		fmt.Println("  No active transfers.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "SERVER\tSNAPSHOT\tSTATE\tPROGRESS\t")

	for _, job := range jobs {
		progress := fmt.Sprintf("%.2fG / %.2fG",
			float64(job.BytesMoved)/1073741824.0,
			float64(job.TotalSize)/1073741824.0,
		)
		if job.TotalSize == 0 {
			progress = "Calculating..."
		}
		name := job.ServerName
		if len(name) > 15 {
			name = name[:12] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t\n", name, job.Snapshot, job.State, progress)
	}
	w.Flush()
	return nil
}
