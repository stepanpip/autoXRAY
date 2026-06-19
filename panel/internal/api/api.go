package api

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"autoxray-panel/internal/clients"
	"autoxray-panel/internal/node"
	"autoxray-panel/internal/runner"
	"autoxray-panel/internal/stats"
)

type Server struct {
	AxDir   string
	XrayAPI string
	Script  string // path to update_clients.sh
	Store   *stats.Store

	// RunFunc is injectable for tests; defaults to runner.Run.
	RunFunc func(script string, env map[string]string) (string, error)

	mu sync.Mutex
}

type userDTO struct {
	Name     string `json:"name"`
	Tag      string `json:"tag"`
	UUID     string `json:"uuid"`
	SubPath  string `json:"subPath"`
	Up       int64  `json:"up"`
	Down     int64  `json:"down"`
	Total    int64  `json:"total"`
	Online   bool   `json:"online"`
	LastSeen string `json:"lastSeen"`
}

func (s *Server) run(script string, env map[string]string) (string, error) {
	if s.RunFunc != nil {
		return s.RunFunc(script, env)
	}
	return runner.Run(script, env)
}

func maskUUID(u string) string {
	if len(u) <= 8 {
		return u
	}
	return u[:8] + "…"
}

func (s *Server) toDTO(c clients.Client) userDTO {
	t := s.Store.Get(c.Name)
	seen := ""
	if !t.LastSeen.IsZero() {
		seen = t.LastSeen.Format(time.RFC3339)
	}
	return userDTO{
		Name: c.Name, Tag: "@" + c.Name, UUID: maskUUID(c.UUID), SubPath: c.SubPath,
		Up: t.Up, Down: t.Down, Total: t.Total(), Online: t.Online(), LastSeen: seen,
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/users", s.listUsers)
	mux.HandleFunc("GET /api/users/{name}", s.getUser)
	mux.HandleFunc("POST /api/users", s.addUser)
	mux.HandleFunc("DELETE /api/users/{name}", s.deleteUser)
	mux.HandleFunc("GET /api/node", s.nodeLoad)
	return mux
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	cs, err := clients.Parse(s.AxDir)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	out := make([]userDTO, 0, len(cs))
	for _, c := range cs {
		out = append(out, s.toDTO(c))
	}
	writeJSON(w, 200, out)
}

func (s *Server) getUser(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cs, err := clients.Parse(s.AxDir)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	for _, c := range cs {
		if c.Name == name {
			writeJSON(w, 200, s.toDTO(c))
			return
		}
	}
	writeJSON(w, 404, map[string]string{"error": "not found"})
}

func (s *Server) addUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	if !clients.ValidateName(body.Name) {
		writeJSON(w, 400, map[string]string{"error": "invalid name"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := clients.Add(s.AxDir, body.Name); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if out, err := s.run(s.Script, map[string]string{"AX_DIR": s.AxDir}); err != nil {
		_ = clients.Delete(s.AxDir, body.Name) // rollback
		writeJSON(w, 500, map[string]string{"error": err.Error(), "output": out})
		return
	}
	cs, _ := clients.Parse(s.AxDir)
	for _, c := range cs {
		if c.Name == body.Name {
			writeJSON(w, 200, s.toDTO(c))
			return
		}
	}
	writeJSON(w, 200, userDTO{Name: body.Name, Tag: "@" + body.Name})
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := clients.Delete(s.AxDir, name); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if out, err := s.run(s.Script, map[string]string{"AX_DIR": s.AxDir}); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error(), "output": out})
		return
	}
	writeJSON(w, 200, map[string]string{"deleted": name})
}

func (s *Server) nodeLoad(w http.ResponseWriter, r *http.Request) {
	load, err := node.Load()
	if err != nil {
		writeJSON(w, 200, map[string]float64{"load": 0})
		return
	}
	writeJSON(w, 200, map[string]float64{"load": load})
}
