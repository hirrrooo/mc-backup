package engine

import "testing"

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
