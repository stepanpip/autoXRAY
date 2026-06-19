package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"autoxray-panel/internal/stats"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "clients.txt"), []byte("alice\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "clients"), 0o755)
	os.WriteFile(filepath.Join(dir, "clients", "alice.env"),
		[]byte("CLIENT_NAME='alice'\nxray_uuid_vrv='uuid-a'\npath_subpage='alice'\n"), 0o644)
	st, _ := stats.Load(filepath.Join(dir, "traffic.json"))
	st.Apply(map[string]stats.Snapshot{"alice": {Up: 100, Down: 200}})
	s := &Server{AxDir: dir, Store: st, Script: "update_clients.sh"}
	s.RunFunc = func(string, map[string]string) (string, error) { return "ok", nil }
	return s
}

func TestListUsers(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("GET", "/api/users", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code %d", w.Code)
	}
	var got []map[string]any
	json.Unmarshal(w.Body.Bytes(), &got)
	if len(got) != 1 || got[0]["name"] != "alice" {
		t.Fatalf("bad body: %s", w.Body.String())
	}
	if got[0]["total"].(float64) != 300 {
		t.Fatalf("want total 300, got %v", got[0]["total"])
	}
}

func TestAddUser(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("POST", "/api/users", strings.NewReader(`{"name":"bob"}`))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code %d body %s", w.Code, w.Body.String())
	}
	data, _ := os.ReadFile(filepath.Join(s.AxDir, "clients.txt"))
	if !strings.Contains(string(data), "bob") {
		t.Fatalf("bob not in clients.txt: %s", data)
	}
}

func TestAddUserRollsBackOnScriptFailure(t *testing.T) {
	s := newTestServer(t)
	s.RunFunc = func(string, map[string]string) (string, error) {
		return "boom", os.ErrPermission
	}
	req := httptest.NewRequest("POST", "/api/users", strings.NewReader(`{"name":"bob"}`))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 500 {
		t.Fatalf("want 500, got %d", w.Code)
	}
	data, _ := os.ReadFile(filepath.Join(s.AxDir, "clients.txt"))
	if strings.Contains(string(data), "bob") {
		t.Fatalf("bob should have been rolled back: %s", data)
	}
}

func TestAddUserRejectsBadName(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("POST", "/api/users", strings.NewReader(`{"name":"bad name"}`))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestDeleteUser(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("DELETE", "/api/users/alice", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code %d", w.Code)
	}
	data, _ := os.ReadFile(filepath.Join(s.AxDir, "clients.txt"))
	if strings.Contains(string(data), "alice") {
		t.Fatalf("alice not deleted: %s", data)
	}
}

var _ = http.MethodGet
