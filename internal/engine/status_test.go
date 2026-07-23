package engine

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestJobTrackerSnapshotReturnsCopies(t *testing.T) {
	tracker := NewJobTracker()
	tracker.Add("minecraft/creative", &JobInfo{
		ServerName: "creative",
		Snapshot:   "20250611-1200",
		State:      "Saving",
		BytesMoved: 1024,
		TotalSize:  2048,
	})

	snap := tracker.Snapshot()
	snap["minecraft/creative"].State = "mutated"
	delete(snap, "minecraft/creative")

	fresh := tracker.Snapshot()
	job, ok := fresh["minecraft/creative"]
	if !ok {
		t.Fatal("snapshot mutation removed job from tracker")
	}
	if job.State != "Saving" {
		t.Errorf("snapshot mutation changed tracker state: got %q", job.State)
	}
}

func TestJobTrackerRemoveDeletesJob(t *testing.T) {
	tracker := NewJobTracker()
	tracker.Add("minecraft/creative", &JobInfo{ServerName: "creative"})

	tracker.Remove("minecraft/creative")

	if got := tracker.Snapshot(); len(got) != 0 {
		t.Fatalf("Snapshot() returned %d jobs after Remove, want 0", len(got))
	}
}

func TestStatusAPIAuth(t *testing.T) {
	canceled := false
	scanned := false
	backedUp := false
	serverParam := ""
	offlineParam := false

	callbacks := StatusCallbacks{
		OnCancel: func() { canceled = true },
		OnScan:   func() { scanned = true },
		OnBackup: func(s string, o bool) {
			backedUp = true
			serverParam = s
			offlineParam = o
		},
	}

	jt := NewJobTracker()

	t.Run("token configured", func(t *testing.T) {
		handler := newStatusMux(jt, callbacks, func() string { return "secret-token" })

		mutatingRoutes := []struct {
			path string
		}{
			{"/backup"},
			{"/cancel"},
			{"/scan"},
		}

		for _, r := range mutatingRoutes {
			// Missing header -> 401
			req := httptest.NewRequest(http.MethodPost, r.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s missing header: got code %d, want %d", r.path, rec.Code, http.StatusUnauthorized)
			}

			// Wrong token -> 401
			req = httptest.NewRequest(http.MethodPost, r.path, nil)
			req.Header.Set("Authorization", "Bearer wrong-token")
			rec = httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s wrong token: got code %d, want %d", r.path, rec.Code, http.StatusUnauthorized)
			}

			// Non-Bearer scheme -> 401
			req = httptest.NewRequest(http.MethodPost, r.path, nil)
			req.Header.Set("Authorization", "Basic secret-token")
			rec = httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s non-bearer scheme: got code %d, want %d", r.path, rec.Code, http.StatusUnauthorized)
			}

			// Wrong method (GET) -> 405 Method Not Allowed
			req = httptest.NewRequest(http.MethodGet, r.path, nil)
			req.Header.Set("Authorization", "Bearer secret-token")
			rec = httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s GET method: got code %d, want %d", r.path, rec.Code, http.StatusMethodNotAllowed)
			}
		}

		// Read-only routes unauthenticated -> 200 OK
		for _, path := range []string{"/status", "/health"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("%s read-only route: got code %d, want %d", path, rec.Code, http.StatusOK)
			}
		}

		// Valid token -> 200 OK and callbacks executed
		canceled = false
		req := httptest.NewRequest(http.MethodPost, "/cancel", nil)
		req.Header.Set("Authorization", "Bearer secret-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !canceled {
			t.Errorf("/cancel valid token: code=%d, canceled=%v", rec.Code, canceled)
		}

		scanned = false
		req = httptest.NewRequest(http.MethodPost, "/scan", nil)
		req.Header.Set("Authorization", "Bearer secret-token")
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !scanned {
			t.Errorf("/scan valid token: code=%d, scanned=%v", rec.Code, scanned)
		}

		backedUp = false
		req = httptest.NewRequest(http.MethodPost, "/backup?server=survival&offline=true", nil)
		req.Header.Set("Authorization", "Bearer secret-token")
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !backedUp || serverParam != "survival" || !offlineParam {
			t.Errorf("/backup valid token: code=%d, backedUp=%v, server=%s, offline=%v", rec.Code, backedUp, serverParam, offlineParam)
		}
	})

	t.Run("token empty (loopback default)", func(t *testing.T) {
		handler := newStatusMux(jt, callbacks, func() string { return "" })

		canceled = false
		req := httptest.NewRequest(http.MethodPost, "/cancel", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !canceled {
			t.Errorf("/cancel empty token: code=%d, canceled=%v", rec.Code, canceled)
		}
	})
}

func TestStatusAPITokenRotation(t *testing.T) {
	var currentToken atomicConfig
	// Initial state: empty token
	currentToken.Store(&Config{
		Global: GlobalConfig{
			APIToken: "",
		},
	})

	canceled := false
	callbacks := StatusCallbacks{
		OnCancel: func() { canceled = true },
	}

	jt := NewJobTracker()
	handler := newStatusMux(jt, callbacks, func() string {
		if cfg := currentToken.Load(); cfg != nil {
			return cfg.Global.APIToken
		}
		return ""
	})

	// 1. Initial empty token permits local POST without Authorization header
	canceled = false
	req := httptest.NewRequest(http.MethodPost, "/cancel", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !canceled {
		t.Fatalf("step 1 empty token: got code %d, canceled=%v, want 200 OK", rec.Code, canceled)
	}

	// 2. Rotate token to secret-v1
	currentToken.Store(&Config{
		Global: GlobalConfig{
			APIToken: "secret-v1",
		},
	})

	// Missing header -> 401
	canceled = false
	req = httptest.NewRequest(http.MethodPost, "/cancel", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("step 2 missing token: got code %d, want 401", rec.Code)
	}

	// Wrong token -> 401
	canceled = false
	req = httptest.NewRequest(http.MethodPost, "/cancel", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("step 2 wrong token: got code %d, want 401", rec.Code)
	}

	// Valid secret-v1 -> 200
	canceled = false
	req = httptest.NewRequest(http.MethodPost, "/cancel", nil)
	req.Header.Set("Authorization", "Bearer secret-v1")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !canceled {
		t.Fatalf("step 2 valid secret-v1: got code %d, canceled=%v, want 200", rec.Code, canceled)
	}

	// 3. Rotate token to secret-v2 without server restart
	currentToken.Store(&Config{
		Global: GlobalConfig{
			APIToken: "secret-v2",
		},
	})

	// Old token secret-v1 -> 401
	canceled = false
	req = httptest.NewRequest(http.MethodPost, "/cancel", nil)
	req.Header.Set("Authorization", "Bearer secret-v1")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("step 3 old token secret-v1: got code %d, want 401", rec.Code)
	}

	// New token secret-v2 -> 200
	canceled = false
	req = httptest.NewRequest(http.MethodPost, "/cancel", nil)
	req.Header.Set("Authorization", "Bearer secret-v2")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !canceled {
		t.Fatalf("step 3 new token secret-v2: got code %d, canceled=%v, want 200", rec.Code, canceled)
	}
}

func TestPrintDashboard(t *testing.T) {
	jt := NewJobTracker()
	jt.Add("minecraft/verylongservernamehere", &JobInfo{
		ServerName: "verylongservernamehere",
		Snapshot:   "20250611-1200",
		State:      "Saving",
		BytesMoved: 1073741824,
		TotalSize:  2147483648,
	})
	jt.Add("minecraft/calculating", &JobInfo{
		ServerName: "calc",
		Snapshot:   "20250611-1300",
		State:      "Saving",
		BytesMoved: 0,
		TotalSize:  0,
	})

	srv := httptest.NewServer(newStatusMux(jt, StatusCallbacks{}, func() string { return "" }))
	defer srv.Close()

	addr := srv.Listener.Addr().String()
	if err := PrintDashboard(addr); err != nil {
		t.Fatalf("PrintDashboard failed: %v", err)
	}

	emptyJt := NewJobTracker()
	emptySrv := httptest.NewServer(newStatusMux(emptyJt, StatusCallbacks{}, func() string { return "" }))
	defer emptySrv.Close()

	if err := PrintDashboard(emptySrv.Listener.Addr().String()); err != nil {
		t.Fatalf("PrintDashboard with empty jobs failed: %v", err)
	}
}
