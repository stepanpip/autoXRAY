package stats

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

type Snapshot struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

type Totals struct {
	Up        int64     `json:"up"`
	Down      int64     `json:"down"`
	LastDelta int64     `json:"lastDelta"`
	LastSeen  time.Time `json:"lastSeen"`
}

func (t Totals) Total() int64 { return t.Up + t.Down }
func (t Totals) Online() bool { return t.LastDelta > 0 }

type Store struct {
	path string
	mu   sync.Mutex
	data map[string]*Totals
}

func Load(path string) (*Store, error) {
	s := &Store{path: path, data: map[string]*Totals{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &s.data); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Apply adds each per-client delta snapshot to the running total.
func (s *Store) Apply(snaps map[string]Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for name, snap := range snaps {
		t := s.data[name]
		if t == nil {
			t = &Totals{}
			s.data[name] = t
		}
		t.Up += snap.Up
		t.Down += snap.Down
		t.LastDelta = snap.Up + snap.Down
		if t.LastDelta > 0 {
			t.LastSeen = now
		}
	}
	_ = s.save()
}

func (s *Store) Get(name string) Totals {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.data[name]; t != nil {
		return *t
	}
	return Totals{}
}

// Delete removes a client's accumulated totals (e.g. when the user is deleted).
func (s *Store) Delete(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, name)
	_ = s.save()
}

func (s *Store) save() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
