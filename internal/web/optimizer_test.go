package web

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/AmirAM03/velox/internal/storage"
)

// Each profile's auto-test is due on its own interval since its last test, with no
// global cycle: the Optimizer only reads per-profile settings.
func TestDueProfilesUsesEachProfilesInterval(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "due.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mk := func(kind string, port int, auto bool, iv string) int64 {
		id, err := store.SaveProfile(storage.Profile{Kind: kind, Name: kind, Port: port, Username: "u", Password: "p", Enabled: true, AutoTest: auto, TestInterval: iv})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	setLastTest := func(id int64, ago time.Duration) {
		b, _ := json.Marshal(ProfileStats{At: time.Now().Add(-ago)})
		if err := store.SetSetting(statsKey(id), string(b)); err != nil {
			t.Fatal(err)
		}
	}
	due15 := mk("a", 30001, true, "15m") // tested 20m ago -> due
	notDue := mk("b", 30002, true, "1h") // tested 5m ago -> not due
	off := mk("c", 30003, false, "5m")   // auto off -> never due
	never := mk("d", 30004, true, "30m") // never tested -> due
	setLastTest(due15, 20*time.Minute)
	setLastTest(notDue, 5*time.Minute)
	setLastTest(off, time.Hour)

	due := (&Optimizer{store: store}).dueProfiles()
	if !due[due15] || due[notDue] || due[off] || !due[never] {
		t.Fatalf("due = %v; want only %d (interval elapsed) and %d (never tested)", due, due15, never)
	}
}

// A run already in progress or queued must block any new run request, so a live
// benchmark is never raced by a second one.
func TestOptimizerRefusesOverlappingRuns(t *testing.T) {
	o := &Optimizer{phase: "idle", pendIDs: map[int64]bool{}, wake: make(chan struct{}, 1)}

	if err := o.TryTriggerProfile(1); err != nil {
		t.Fatalf("first request should be accepted: %v", err)
	}
	if err := o.TryTriggerProfile(2); err == nil {
		t.Error("a second request while one is queued must be refused")
	}
	if err := o.TryTriggerRefresh(); err == nil {
		t.Error("a manual refresh while a test is queued must be refused")
	}

	// The run finishes: queue drained, back to idle. A new run is accepted again.
	o.pendIDs = map[int64]bool{}
	o.phase = "idle"
	if err := o.TryTriggerRefresh(); err != nil {
		t.Errorf("after finishing, a new run should be accepted: %v", err)
	}

	// A run in progress (phase not idle) also refuses, even with an empty queue.
	o.pendRefresh, o.pendIDs = false, map[int64]bool{}
	o.phase = "testing"
	if err := o.TryTriggerProfile(3); err == nil {
		t.Error("a request while a run is in progress must be refused")
	}
}
