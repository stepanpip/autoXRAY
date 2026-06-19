package stats

import (
	"path/filepath"
	"testing"
)

func TestApplyAccumulates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Apply(map[string]Snapshot{"alice": {Up: 100, Down: 200}})
	s.Apply(map[string]Snapshot{"alice": {Up: 50, Down: 25}})
	tot := s.Get("alice")
	if tot.Up != 150 || tot.Down != 225 {
		t.Fatalf("want 150/225, got %d/%d", tot.Up, tot.Down)
	}
	if tot.LastDelta != 75 { // 50+25
		t.Fatalf("want lastDelta 75, got %d", tot.LastDelta)
	}
}

func TestPersistAcrossLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic.json")
	s, _ := Load(path)
	s.Apply(map[string]Snapshot{"bob": {Up: 10, Down: 20}})
	s2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.Get("bob"); got.Up != 10 || got.Down != 20 {
		t.Fatalf("not persisted: %+v", got)
	}
}

func TestZeroDeltaKeepsTotal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic.json")
	s, _ := Load(path)
	s.Apply(map[string]Snapshot{"a": {Up: 5, Down: 5}})
	s.Apply(map[string]Snapshot{"a": {Up: 0, Down: 0}})
	tot := s.Get("a")
	if tot.Up != 5 || tot.LastDelta != 0 {
		t.Fatalf("want total 5 lastDelta 0, got %d/%d", tot.Up, tot.LastDelta)
	}
}

func TestAbsentClientGoesOffline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic.json")
	s, _ := Load(path)
	s.Apply(map[string]Snapshot{"alice": {Up: 10, Down: 10}})
	if !s.Get("alice").Online() {
		t.Fatal("alice should be online after active interval")
	}
	// Next poll has no traffic for alice (she is absent from the snapshot).
	s.Apply(map[string]Snapshot{"bob": {Up: 1, Down: 1}})
	alice := s.Get("alice")
	if alice.Online() {
		t.Fatalf("alice should be offline when absent, lastDelta=%d", alice.LastDelta)
	}
	if alice.Up != 10 || alice.Down != 10 {
		t.Fatalf("alice totals must be kept, got %+v", alice)
	}
}

func TestParseStatsJSON(t *testing.T) {
	raw := []byte(`{"stat":[
		{"name":"user>>>alice>>>traffic>>>uplink","value":"100"},
		{"name":"user>>>alice>>>traffic>>>downlink","value":"250"},
		{"name":"user>>>bob>>>traffic>>>uplink","value":"7"}
	]}`)
	got, err := parseStatsJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got["alice"].Up != 100 || got["alice"].Down != 250 {
		t.Fatalf("alice wrong: %+v", got["alice"])
	}
	if got["bob"].Up != 7 || got["bob"].Down != 0 {
		t.Fatalf("bob wrong: %+v", got["bob"])
	}
}

func TestParseStatsJSONEmpty(t *testing.T) {
	got, err := parseStatsJSON([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %d", len(got))
	}
}

func TestDeleteRemovesEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic.json")
	s, _ := Load(path)
	s.Apply(map[string]Snapshot{"alice": {Up: 5, Down: 5}})
	s.Delete("alice")
	if got := s.Get("alice"); got.Up != 0 || got.Down != 0 {
		t.Fatalf("entry should be gone, got %+v", got)
	}
	// persisted
	s2, _ := Load(path)
	if got := s2.Get("alice"); got.Up != 0 {
		t.Fatalf("delete not persisted: %+v", got)
	}
}
