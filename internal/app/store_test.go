package app

import (
	"path/filepath"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "nested", "state.json"))
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.Settings.CheckTime != "03:00" || state.Stacks == nil {
		t.Fatalf("unexpected default state: %#v", state)
	}
	state.Settings.CheckTime = "06:30"
	state.Stacks["demo"] = StackState{ID: "demo", Name: "demo"}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Settings.CheckTime != "06:30" || loaded.Stacks["demo"].Name != "demo" {
		t.Fatalf("state did not round trip: %#v", loaded)
	}
}
