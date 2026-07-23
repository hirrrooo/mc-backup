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
		handler := newStatusMux(jt, callbacks, "secret-token")

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
		handler := newStatusMux(jt, callbacks, "")

		canceled = false
		req := httptest.NewRequest(http.MethodPost, "/cancel", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !canceled {
			t.Errorf("/cancel empty token: code=%d, canceled=%v", rec.Code, canceled)
		}
	})
}
