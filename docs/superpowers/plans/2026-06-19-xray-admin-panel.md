# autoXRAY Admin Panel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a minimal Go web panel to add/delete autoXRAY-multi clients and view real per-user traffic via the xray Stats API.

**Architecture:** Single Go binary (`panel/`) serving an embedded HTML frontend, listening on `127.0.0.1:8088` behind nginx basic-auth. It reuses the autoXRAY-multi model: lists clients from `clients/*.env`, mutates `clients.txt` and runs `update_clients.sh` for add/delete, and polls `xray api statsquery` into a persisted traffic accumulator.

**Tech Stack:** Go 1.22+ (stdlib `net/http` method routing, `embed`), vanilla HTML/CSS/JS frontend, bash (`enable_stats.sh`), systemd, nginx.

---

## File Structure

```
panel/
  go.mod                       module autoxray-panel (go 1.22)
  main.go                      wiring: load store, start poller, http server
  internal/
    clients/clients.go         Client model, Parse, Add, Delete, ValidateName
    clients/clients_test.go
    stats/store.go             persisted traffic accumulator
    stats/store_test.go
    stats/query.go             xray api statsquery wrapper + parse
    stats/query_test.go
    runner/runner.go           run update_clients.sh, capture output/error
    runner/runner_test.go
    api/api.go                 HTTP handlers (Server struct)
    api/api_test.go
    node/node.go               /proc/loadavg reader
  web/index.html               embedded frontend
  enable_stats.sh              one-time idempotent config.json patch
  xray-panel.service           systemd unit
  panel.env.example            env template
  Makefile                     build linux/amd64
  deploy.md                    server install + manual test checklist
```

Plus modify `autoXRAY-multi/autoxray_lib.sh` (add `email` to xray clients).

**Type contract (used across tasks):**
```go
// clients
type Client struct { Name, UUID, SubPath string }
// stats
type Snapshot struct { Up, Down int64 }            // delta since last reset-poll
type Totals   struct { Up, Down, LastDelta int64; LastSeen time.Time }
```
Stats are polled with `reset` so each query returns a delta; the store accumulates deltas. `Online` is derived as `LastDelta > 0`.

---

## Task 1: Go module scaffold

**Files:**
- Create: `panel/go.mod`
- Create: `panel/main.go` (temporary skeleton)

- [ ] **Step 1: Create go.mod**

`panel/go.mod`:
```
module autoxray-panel

go 1.22
```

- [ ] **Step 2: Minimal main**

`panel/main.go`:
```go
package main

import "fmt"

func main() {
	fmt.Println("xray-panel")
}
```

- [ ] **Step 3: Verify it builds**

Run: `cd panel && go build ./...`
Expected: no output, exit 0.

- [ ] **Step 4: Commit**

```bash
git add panel/go.mod panel/main.go
git commit -m "chore(panel): scaffold go module"
```

---

## Task 2: clients package — parse model

**Files:**
- Create: `panel/internal/clients/clients.go`
- Test: `panel/internal/clients/clients_test.go`

Model mirrors `autoxray_lib.sh`: `clients.txt` is the allowed-names list (comments with `#`, names trimmed); each `clients/<name>.env` has `CLIENT_NAME`, `xray_uuid_vrv`, `path_subpage`. `Parse` returns clients whose env exists AND whose name is in clients.txt, sorted by name.

- [ ] **Step 1: Write failing test**

`panel/internal/clients/clients_test.go`:
```go
package clients

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) string {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "clients.txt"), "# names\nalice\nbob\n")
	writeFile(t, filepath.Join(dir, "clients", "alice.env"),
		"CLIENT_NAME='alice'\nxray_uuid_vrv='uuid-a'\npath_subpage='alice'\n")
	writeFile(t, filepath.Join(dir, "clients", "bob.env"),
		"CLIENT_NAME='bob'\nxray_uuid_vrv='uuid-b'\npath_subpage='bob'\n")
	return dir
}

func TestParse(t *testing.T) {
	dir := fixture(t)
	got, err := Parse(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 clients, got %d", len(got))
	}
	if got[0].Name != "alice" || got[0].UUID != "uuid-a" || got[0].SubPath != "alice" {
		t.Fatalf("bad first client: %+v", got[0])
	}
}

func TestParseSkipsEnvNotInClientsTxt(t *testing.T) {
	dir := fixture(t)
	// stale env not listed in clients.txt
	writeFile(t, filepath.Join(dir, "clients", "ghost.env"),
		"CLIENT_NAME='ghost'\nxray_uuid_vrv='uuid-g'\npath_subpage='ghost'\n")
	got, err := Parse(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("ghost should be skipped, got %d", len(got))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd panel && go test ./internal/clients/`
Expected: FAIL — `undefined: Parse`.

- [ ] **Step 3: Implement Parse**

`panel/internal/clients/clients.go`:
```go
package clients

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Client struct {
	Name    string
	UUID    string
	SubPath string
}

var nameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

func ValidateName(n string) bool { return nameRe.MatchString(n) }

func clientsTxtPath(axDir string) string { return filepath.Join(axDir, "clients.txt") }
func clientsDir(axDir string) string     { return filepath.Join(axDir, "clients") }

// AllowedNames reads clients.txt, skipping blank lines and #-comments.
func AllowedNames(axDir string) ([]string, error) {
	f, err := os.Open(clientsTxtPath(axDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.ReplaceAll(sc.Text(), "\r", "")
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out, sc.Err()
}

func parseEnv(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	d := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		k, v, _ := strings.Cut(line, "=")
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `'"`)
		d[strings.TrimSpace(k)] = v
	}
	return d, sc.Err()
}

// Parse returns clients present in clients/*.env AND listed in clients.txt.
func Parse(axDir string) ([]Client, error) {
	names, err := AllowedNames(axDir)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, n := range names {
		allowed[n] = true
	}
	matches, err := filepath.Glob(filepath.Join(clientsDir(axDir), "*.env"))
	if err != nil {
		return nil, err
	}
	var out []Client
	for _, p := range matches {
		env, err := parseEnv(p)
		if err != nil {
			return nil, err
		}
		name := env["CLIENT_NAME"]
		if name == "" || !allowed[name] {
			continue
		}
		out = append(out, Client{Name: name, UUID: env["xray_uuid_vrv"], SubPath: env["path_subpage"]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd panel && go test ./internal/clients/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add panel/internal/clients/
git commit -m "feat(panel): clients parse model"
```

---

## Task 3: clients package — Add / Delete

**Files:**
- Modify: `panel/internal/clients/clients.go`
- Modify: `panel/internal/clients/clients_test.go`

`Add` validates the name, rejects duplicates (case-sensitive, matching `ax_validate_client_name`/dup check), appends `name\n` to clients.txt. `Delete` rewrites clients.txt without the matching name line (preserving comments/other lines). Neither runs update_clients.sh — that is the runner's job.

- [ ] **Step 1: Write failing tests**

Append to `panel/internal/clients/clients_test.go`:
```go
func TestAddAppendsName(t *testing.T) {
	dir := fixture(t)
	if err := Add(dir, "carol"); err != nil {
		t.Fatal(err)
	}
	names, _ := AllowedNames(dir)
	found := false
	for _, n := range names {
		if n == "carol" {
			found = true
		}
	}
	if !found {
		t.Fatalf("carol not added: %v", names)
	}
}

func TestAddRejectsBadName(t *testing.T) {
	dir := fixture(t)
	if err := Add(dir, "bad name!"); err == nil {
		t.Fatal("expected error for invalid name")
	}
}

func TestAddRejectsDuplicate(t *testing.T) {
	dir := fixture(t)
	if err := Add(dir, "alice"); err == nil {
		t.Fatal("expected error for duplicate")
	}
}

func TestDeleteRemovesName(t *testing.T) {
	dir := fixture(t)
	if err := Delete(dir, "alice"); err != nil {
		t.Fatal(err)
	}
	names, _ := AllowedNames(dir)
	for _, n := range names {
		if n == "alice" {
			t.Fatalf("alice still present: %v", names)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd panel && go test ./internal/clients/`
Expected: FAIL — `undefined: Add`, `undefined: Delete`.

- [ ] **Step 3: Implement Add / Delete**

First, add `"fmt"` to the import block at the top of `clients.go` so it reads:
```go
import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)
```
Then append the functions:
```go
// Add validates name, rejects duplicates, appends to clients.txt.
func Add(axDir, name string) error {
	if !ValidateName(name) {
		return fmt.Errorf("invalid client name %q (allowed: a-z A-Z 0-9 _ -)", name)
	}
	names, err := AllowedNames(axDir)
	if err != nil {
		return err
	}
	for _, n := range names {
		if n == name {
			return fmt.Errorf("client %q already exists", name)
		}
	}
	f, err := os.OpenFile(clientsTxtPath(axDir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s\n", name)
	return err
}

// Delete rewrites clients.txt without the line whose trimmed name == name.
func Delete(axDir, name string) error {
	path := clientsTxtPath(axDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var kept []string
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.ReplaceAll(raw, "\r", "")
		t := strings.TrimSpace(line)
		if t != "" && !strings.HasPrefix(t, "#") {
			n := t
			if i := strings.Index(n, "#"); i >= 0 {
				n = strings.TrimSpace(n[:i])
			}
			if n == name {
				continue
			}
		}
		kept = append(kept, line)
	}
	out := strings.Join(kept, "\n")
	return os.WriteFile(path, []byte(out), 0o644)
}
```
Note: the `import_extra_marker` line above is illustrative — do not add it; just ensure the import block matches and `fmt` is imported.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd panel && go test ./internal/clients/`
Expected: PASS (all 6 tests).

- [ ] **Step 5: Commit**

```bash
git add panel/internal/clients/
git commit -m "feat(panel): clients add/delete"
```

---

## Task 4: runner package — run update_clients.sh

**Files:**
- Create: `panel/internal/runner/runner.go`
- Test: `panel/internal/runner/runner_test.go`

`Run(script string, env map[string]string)` executes the script with `/bin/bash`, merges env over os.Environ, captures combined stdout+stderr, returns `(output, error)`. Non-zero exit → error wrapping output. Tests use a stub bash script in a temp dir (skip on Windows via `runtime.GOOS`).

- [ ] **Step 1: Write failing tests**

`panel/internal/runner/runner_test.go`:
```go
package runner

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func stub(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "stub.sh")
	if err := os.WriteFile(p, []byte("#!/bin/bash\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs bash")
	}
	out, err := Run(stub(t, `echo "hello $AX_DIR"`), map[string]string{"AX_DIR": "/tmp/x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello /tmp/x") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestRunFailureCapturesOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs bash")
	}
	_, err := Run(stub(t, `echo "boom" >&2; exit 3`), nil)
	if err == nil {
		t.Fatal("expected error on non-zero exit")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error should include output: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd panel && go test ./internal/runner/`
Expected: FAIL — `undefined: Run`.

- [ ] **Step 3: Implement Run**

`panel/internal/runner/runner.go`:
```go
package runner

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
)

// Run executes script via bash, with extra env merged over the process env.
// Returns combined output; on non-zero exit returns an error wrapping output.
func Run(script string, env map[string]string) (string, error) {
	cmd := exec.Command("/bin/bash", script)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if err != nil {
		return out, fmt.Errorf("%s failed: %v\n%s", script, err, out)
	}
	return out, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd panel && go test ./internal/runner/`
Expected: PASS (skipped on Windows).

- [ ] **Step 5: Commit**

```bash
git add panel/internal/runner/
git commit -m "feat(panel): script runner"
```

---

## Task 5: stats package — accumulator store

**Files:**
- Create: `panel/internal/stats/store.go`
- Test: `panel/internal/stats/store_test.go`

Store holds per-client `Totals`, persisted as JSON at `<axDir>/panel_traffic.json`. `Apply(map[string]Snapshot)` adds each delta to the running total, sets `LastDelta`, and bumps `LastSeen` when delta>0, then saves. `Get(name)` returns the totals. Concurrency-safe via mutex.

- [ ] **Step 1: Write failing tests**

`panel/internal/stats/store_test.go`:
```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd panel && go test ./internal/stats/`
Expected: FAIL — `undefined: Load`.

- [ ] **Step 3: Implement store**

`panel/internal/stats/store.go`:
```go
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

func (t Totals) Total() int64  { return t.Up + t.Down }
func (t Totals) Online() bool  { return t.LastDelta > 0 }

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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd panel && go test ./internal/stats/`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add panel/internal/stats/store.go panel/internal/stats/store_test.go
git commit -m "feat(panel): traffic accumulator store"
```

---

## Task 6: stats package — xray statsquery parse

**Files:**
- Create: `panel/internal/stats/query.go`
- Modify: `panel/internal/stats/store_test.go` (add parse test in same package)

`parseStatsJSON([]byte) map[string]Snapshot` parses the JSON emitted by
`xray api statsquery` (`{"stat":[{"name":"user>>>alice>>>traffic>>>uplink","value":"123"}]}`),
grouping by user and direction. `Query(server string) (map[string]Snapshot, error)`
shells out to `xray api statsquery --server=<server> -pattern "user>>>" -reset` and parses stdout. Parsing is unit-tested; the live `Query` is exercised manually on the server.

- [ ] **Step 1: Write failing test**

Append to `panel/internal/stats/store_test.go`:
```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd panel && go test ./internal/stats/`
Expected: FAIL — `undefined: parseStatsJSON`.

- [ ] **Step 3: Implement query.go**

`panel/internal/stats/query.go`:
```go
package stats

import (
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
)

type statEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type statResp struct {
	Stat []statEntry `json:"stat"`
}

// parseStatsJSON groups "user>>>NAME>>>traffic>>>uplink|downlink" counters by user.
func parseStatsJSON(b []byte) (map[string]Snapshot, error) {
	var r statResp
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	out := map[string]Snapshot{}
	for _, e := range r.Stat {
		parts := strings.Split(e.Name, ">>>")
		if len(parts) != 4 || parts[0] != "user" || parts[2] != "traffic" {
			continue
		}
		name, dir := parts[1], parts[3]
		v, _ := strconv.ParseInt(e.Value, 10, 64)
		s := out[name]
		switch dir {
		case "uplink":
			s.Up += v
		case "downlink":
			s.Down += v
		}
		out[name] = s
	}
	return out, nil
}

// Query runs xray statsquery with reset and returns per-user deltas.
func Query(server string) (map[string]Snapshot, error) {
	cmd := exec.Command("xray", "api", "statsquery",
		"--server="+server, "-pattern", "user>>>", "-reset")
	b, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseStatsJSON(b)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd panel && go test ./internal/stats/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add panel/internal/stats/query.go panel/internal/stats/store_test.go
git commit -m "feat(panel): xray statsquery parse"
```

---

## Task 7: node package — load average

**Files:**
- Create: `panel/internal/node/node.go`
- Test: `panel/internal/node/node_test.go`

`parseLoadAvg(string) (float64, error)` extracts the 1-minute load from `/proc/loadavg` content. `Load() (float64, error)` reads the file (Linux). The parser is unit-tested.

- [ ] **Step 1: Write failing test**

`panel/internal/node/node_test.go`:
```go
package node

import "testing"

func TestParseLoadAvg(t *testing.T) {
	got, err := parseLoadAvg("0.42 0.31 0.25 1/234 5678\n")
	if err != nil {
		t.Fatal(err)
	}
	if got != 0.42 {
		t.Fatalf("want 0.42, got %v", got)
	}
}

func TestParseLoadAvgBad(t *testing.T) {
	if _, err := parseLoadAvg(""); err == nil {
		t.Fatal("expected error on empty input")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd panel && go test ./internal/node/`
Expected: FAIL — `undefined: parseLoadAvg`.

- [ ] **Step 3: Implement node.go**

`panel/internal/node/node.go`:
```go
package node

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func parseLoadAvg(s string) (float64, error) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty loadavg")
	}
	return strconv.ParseFloat(fields[0], 64)
}

// Load returns the 1-minute load average (Linux /proc/loadavg).
func Load() (float64, error) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, err
	}
	return parseLoadAvg(string(b))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd panel && go test ./internal/node/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add panel/internal/node/
git commit -m "feat(panel): node loadavg reader"
```

---

## Task 8: api package — HTTP handlers

**Files:**
- Create: `panel/internal/api/api.go`
- Test: `panel/internal/api/api_test.go`

`Server` holds `AxDir`, `Store *stats.Store`, `XrayAPI string`, `Script string` (path to update_clients.sh), and a `sync.Mutex` serializing mutations. It builds a `userDTO` joining `clients.Client` + `stats.Totals`. Handlers (Go 1.22 method routing):
- `GET /api/users` → list of `userDTO`
- `GET /api/users/{name}` → one `userDTO` (404 if absent)
- `POST /api/users` `{ "name": "..." }` → add (under mutex): `clients.Add`, then `runner.Run(Script)`; rollback `clients.Delete` if the script fails; return the created user.
- `DELETE /api/users/{name}` → delete (under mutex): `clients.Delete`, then `runner.Run(Script)`.
- `GET /api/node` → `{ "load": 0.42 }`.

For `POST`/`DELETE` tests we inject the runner via a `RunFunc func(script string, env map[string]string) (string, error)` field defaulting to `runner.Run`, so tests use a fake (no bash/xray needed).

- [ ] **Step 1: Write failing tests**

`panel/internal/api/api_test.go`:
```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd panel && go test ./internal/api/`
Expected: FAIL — `undefined: Server`.

- [ ] **Step 3: Implement api.go**

`panel/internal/api/api.go`:
```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd panel && go test ./internal/api/`
Expected: PASS (6 tests).

- [ ] **Step 5: Commit**

```bash
git add panel/internal/api/
git commit -m "feat(panel): http api handlers"
```

---

## Task 9: main wiring + embed frontend + poller

**Files:**
- Modify: `panel/main.go`
- Create: `panel/web/index.html` (placeholder until Task 10)

`main` reads env (`AX_DIR`, `PANEL_ADDR`, `XRAY_API`, default `update_clients.sh` path at `<AX_DIR>/autoXRAY-multi/update_clients.sh` — overridable via `UPDATE_SCRIPT`), loads the store, starts a 30s poller goroutine that calls `stats.Query` + `store.Apply` (logging errors, not crashing), serves the embedded `web/` at `/`, and mounts the api handler.

- [ ] **Step 1: Create placeholder frontend**

`panel/web/index.html`:
```html
<!doctype html><meta charset="utf-8"><title>Xray Panel</title>
<body>panel</body>
```

- [ ] **Step 2: Write main.go**

`panel/main.go`:
```go
package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"autoxray-panel/internal/api"
	"autoxray-panel/internal/stats"
)

//go:embed web
var webFS embed.FS

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	axDir := env("AX_DIR", "/usr/local/etc/xray")
	addr := env("PANEL_ADDR", "127.0.0.1:8088")
	xrayAPI := env("XRAY_API", "127.0.0.1:10085")
	script := env("UPDATE_SCRIPT", filepath.Join(axDir, "autoXRAY-multi", "update_clients.sh"))

	store, err := stats.Load(filepath.Join(axDir, "panel_traffic.json"))
	if err != nil {
		log.Fatalf("load store: %v", err)
	}

	go pollLoop(xrayAPI, store)

	srv := &api.Server{AxDir: axDir, XrayAPI: xrayAPI, Script: script, Store: store}

	mux := http.NewServeMux()
	mux.Handle("/api/", srv.Handler())
	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("/", http.FileServer(http.FS(sub)))

	log.Printf("xray-panel on %s (AX_DIR=%s)", addr, axDir)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func pollLoop(xrayAPI string, store *stats.Store) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	poll := func() {
		snaps, err := stats.Query(xrayAPI)
		if err != nil {
			log.Printf("statsquery: %v", err)
			return
		}
		store.Apply(snaps)
	}
	poll()
	for range t.C {
		poll()
	}
}
```

Note: `srv.Handler()` registers paths like `GET /api/users`; mounting it under `mux.Handle("/api/", ...)` works because the inner mux matches the full path. Verify in Step 3.

- [ ] **Step 3: Verify build and run smoke test**

Run: `cd panel && go build ./... && AX_DIR=$(mktemp -d) PANEL_ADDR=127.0.0.1:8099 ./autoxray-panel & sleep 1 && curl -s 127.0.0.1:8099/api/users; kill %1`
Expected: `[]` (empty list, no clients) and no crash. On Windows, run `go build ./...` only (Expected: exit 0).

- [ ] **Step 4: Commit**

```bash
git add panel/main.go panel/web/index.html
git commit -m "feat(panel): main wiring, embed frontend, stats poller"
```

---

## Task 10: Frontend — index.html (pixel-match mockup)

**Files:**
- Modify: `panel/web/index.html`

Build the full single-file frontend matching the handoff mockup
(`docs`/handoff colors: bg `#15171c`, card `#1b1e24`, border `rgba(255,255,255,.07)`,
accents lavender `#b4bee0`, sage `#a3c7b5`, clay `#d6b9a1`; fonts Hanken Grotesk +
JetBrains Mono). Two screens: **Users** (table + search + "Добавить" modal) and
**Detail** (profile, 3 KPIs, 7-day SVG chart, connection block, config link with
Copy + QR, Delete button). Sidebar with logo, active "Пользователи", disabled
"Дашборд"/"Настройки", and a live node-load card fed by `GET /api/node`.

This is presentational (no unit tests); verify by loading in a browser against the running binary.

- [ ] **Step 1: Write index.html**

Replace `panel/web/index.html` with the full implementation. Requirements the file MUST satisfy (implement faithfully to the mockup at `docs/superpowers/specs/2026-06-19-xray-admin-panel-design.md` and the original handoff HTML):

1. `<head>`: charset, viewport, Google Fonts link for `Hanken Grotesk` + `JetBrains Mono`, qrcodejs CDN (`https://cdnjs.cloudflare.com/ajax/libs/qrcodejs/1.0.0/qrcode.min.js`), and a `<style>` block defining the mockup's `*{box-sizing}`, `body{background:#15171c;color:#e7e9ec}`, `@keyframes pulseDot`, scrollbar styles.
2. Layout: flex with a `244px` sidebar (logo block "Xray Panel / v2.8 · core", "МЕНЮ" label, Дашборд button **disabled/opacity .4**, Пользователи button **active** using `rgba(168,180,216,.15)` bg + `#b4bee0` text, node-load card at bottom, Настройки button disabled) and a `<main>` with a `66px` header (title/subtitle, search input `#1b1e24`, "+ Добавить" button styled like the detail "Копировать" button, avatar "АД").
3. Users table: grid columns `1.9fr .95fr 1.05fr .85fr .85fr .85fr 1fr`, header row uppercase `#5b606a`; data rows with avatar-initials chip (cycle the 3 swatches lavender/sage/clay by index), name + `@tag` mono, status dot (online → sage + pulseDot anim; offline → `#454a52`), proto/`—` for location column, down (sage), up (clay), total (white), "—" for expires. Row click → detail.
4. Detail screen: back button, profile card (60px initials chip, name, tag, status + proto badges), 3 KPI cards (Входящий sage / Исходящий clay / Всего accent), 7-day SVG area+line chart built from a `smooth()` cubic path (port the `smooth`/`buildChart` helpers from the handoff `support.js`) using the user's up/down totals split into a synthetic 7-point series (until per-day history exists), connection block (UUID masked, Протокол, Inbound, IP — from API or "—"), config link row with **Копировать** (clipboard) and **QR** (qrcodejs modal), and a **Удалить** button (clay/red outline) → confirm → `DELETE`.
5. Add modal: overlay (`rgba(0,0,0,.88)` + blur), card with name input (placeholder "имя латиницей"), client-side validate `^[a-zA-Z0-9][a-zA-Z0-9_-]*$`, "Создать"/"Отмена". On submit `POST /api/users`; on success refetch list; on error show the returned message inline.
6. JS: `fetch` helpers `getJSON/postJSON/del`; `state = {screen, users, selected, search}`; `render()` re-renders from state; number formatting `fmt(bytes)` → ГБ/ТБ matching the mockup's `fmt` (note mockup `fmt` took GB; here convert bytes → GB first: `gb = bytes/1e9`); poll `/api/node` every 5s to update the load card width/percent; `copyText`/`showQR`/`closeModal` from the handoff.
7. Format helper detail: `function fmtBytes(b){const gb=b/1e9; if(gb>=1000)return (gb/1000).toFixed(2)+' ТБ'; if(gb>=100)return gb.toFixed(0)+' ГБ'; return gb.toFixed(1)+' ГБ';}`.

- [ ] **Step 2: Build and verify in browser**

Run on a Linux box (or dev machine with a fixture `AX_DIR`):
```bash
cd panel && go build -o /tmp/xpanel ./... && \
  AX_DIR=/path/to/fixture PANEL_ADDR=127.0.0.1:8099 /tmp/xpanel
```
Open `http://127.0.0.1:8099/`. Expected: dark panel renders, Users table lists fixture clients, search filters, clicking a row opens detail, Copy/QR work. Compare side-by-side with the handoff mockup for spacing/colors.

- [ ] **Step 3: Commit**

```bash
git add panel/web/index.html
git commit -m "feat(panel): frontend matching handoff mockup"
```

---

## Task 11: Tag xray clients with email (lib change)

**Files:**
- Modify: `autoXRAY-multi/autoxray_lib.sh` (function `ax_patch_xray_clients`, lines ~135-181)

Per-user stats are keyed by `email`. Add `"email": "<CLIENT_NAME>"` to each client entry so `xray api statsquery` reports `user>>>NAME>>>...`. The python block currently builds `vision`/`plain` from UUIDs only; extend it to also read `CLIENT_NAME` and emit email.

- [ ] **Step 1: Update the embedded python in ax_patch_xray_clients**

In `autoXRAY-multi/autoxray_lib.sh`, replace the UUID-collection + client-list build inside the `python3 <<'PY'` heredoc with a version that keeps name+uuid pairs:
```python
import json, os, glob

ax_dir = os.environ.get("AX_DIR", "/usr/local/etc/xray")
cfg_path = os.path.join(ax_dir, "config.json")
clients_dir = os.path.join(ax_dir, "clients")

entries = []  # (email, uuid)
for path in sorted(glob.glob(os.path.join(clients_dir, "*.env"))):
    data = {}
    with open(path) as f:
        for line in f:
            line = line.strip()
            if "=" in line and not line.startswith("#"):
                k, v = line.split("=", 1)
                data[k.strip()] = v.strip().strip("'\"")
    u = data.get("xray_uuid_vrv")
    name = data.get("CLIENT_NAME") or os.path.splitext(os.path.basename(path))[0]
    if u:
        entries.append((name, u))

if not entries:
    raise SystemExit("Нет UUID клиентов в clients/*.env")

with open(cfg_path) as f:
    cfg = json.load(f)

vision = [{"flow": "xtls-rprx-vision", "id": u, "email": e} for e, u in entries]
plain = [{"id": u, "email": e} for e, u in entries]

for ib in cfg.get("inbounds", []):
    if ib.get("protocol") != "vless":
        continue
    st = ib.get("settings") or {}
    if "clients" not in st:
        continue
    cur = st["clients"]
    if not cur:
        continue
    needs_flow = any("flow" in c for c in cur)
    st["clients"] = vision if needs_flow else plain

with open(cfg_path, "w") as f:
    json.dump(cfg, f, indent=2)
print(f"Xray: {len(entries)} клиент(ов) в inbounds vless")
```

- [ ] **Step 2: Verify python syntax**

Run: `python3 -c "import ast,sys; ast.parse(open('/dev/stdin').read())"` is impractical for a heredoc; instead lint the script: `bash -n autoXRAY-multi/autoxray_lib.sh`
Expected: exit 0 (no syntax error). Functional test happens on the server in Task 14.

- [ ] **Step 3: Commit**

```bash
git add autoXRAY-multi/autoxray_lib.sh
git commit -m "feat(multi): tag xray clients with email for per-user stats"
```

---

## Task 12: enable_stats.sh (one-time config patch)

**Files:**
- Create: `panel/enable_stats.sh`

Idempotently add `stats`, `api`, `policy`, an api dokodemo-door inbound on `127.0.0.1:10085`, and the api routing rule to `<AX_DIR>/config.json`. Re-running must not duplicate. Uses python3 (already a dependency).

- [ ] **Step 1: Write enable_stats.sh**

`panel/enable_stats.sh`:
```bash
#!/bin/bash
set -euo pipefail
AX_DIR="${AX_DIR:-/usr/local/etc/xray}"
CFG="$AX_DIR/config.json"
[[ -f "$CFG" ]] || { echo "Нет $CFG"; exit 1; }

CFG="$CFG" python3 <<'PY'
import json, os
cfg_path = os.environ["CFG"]
with open(cfg_path) as f:
    cfg = json.load(f)

cfg["stats"] = cfg.get("stats", {})

api = cfg.get("api") or {}
api["tag"] = "api"
svc = set(api.get("services", []))
svc.add("StatsService")
api["services"] = sorted(svc)
cfg["api"] = api

pol = cfg.get("policy") or {}
levels = pol.get("levels") or {}
lvl0 = levels.get("0") or {}
lvl0["statsUserUplink"] = True
lvl0["statsUserDownlink"] = True
levels["0"] = lvl0
pol["levels"] = levels
cfg["policy"] = pol

inbounds = cfg.setdefault("inbounds", [])
if not any(ib.get("tag") == "api" for ib in inbounds):
    inbounds.append({
        "tag": "api", "protocol": "dokodemo-door",
        "listen": "127.0.0.1", "port": 10085,
        "settings": {"address": "127.0.0.1"},
    })

routing = cfg.setdefault("routing", {})
rules = routing.setdefault("rules", [])
if not any(r.get("inboundTag") == ["api"] or "api" in (r.get("inboundTag") or []) for r in rules):
    rules.insert(0, {"type": "field", "inboundTag": ["api"], "outboundTag": "api"})

with open(cfg_path, "w") as f:
    json.dump(cfg, f, indent=2)
print("config.json: stats/api/policy включены")
PY

if command -v xray >/dev/null && ! xray -test -config "$AX_DIR/config.json" >/dev/null 2>&1; then
    echo "ВНИМАНИЕ: xray -test не прошёл после правки"; exit 1
fi
echo "Готово. Перезапустите: systemctl restart xray"
```

- [ ] **Step 2: Lint**

Run: `bash -n panel/enable_stats.sh`
Expected: exit 0.

- [ ] **Step 3: Make executable + commit**

```bash
chmod +x panel/enable_stats.sh
git add panel/enable_stats.sh
git commit -m "feat(panel): enable_stats.sh one-time config patch"
```

---

## Task 13: Deploy artifacts (systemd, env, Makefile, docs)

**Files:**
- Create: `panel/xray-panel.service`
- Create: `panel/panel.env.example`
- Create: `panel/Makefile`
- Create: `panel/deploy.md`

- [ ] **Step 1: systemd unit**

`panel/xray-panel.service`:
```ini
[Unit]
Description=autoXRAY admin panel
After=network.target xray.service

[Service]
Type=simple
EnvironmentFile=/usr/local/etc/xray/panel.env
ExecStart=/usr/local/bin/xray-panel
Restart=always
RestartSec=3
User=root

[Install]
WantedBy=multi-user.target
```

- [ ] **Step 2: env template**

`panel/panel.env.example`:
```
AX_DIR=/usr/local/etc/xray
PANEL_ADDR=127.0.0.1:8088
XRAY_API=127.0.0.1:10085
UPDATE_SCRIPT=/usr/local/etc/xray/autoXRAY-multi/update_clients.sh
```

- [ ] **Step 3: Makefile**

`panel/Makefile`:
```make
BIN=xray-panel

.PHONY: build test
build:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o $(BIN) .

test:
	go test ./...
```

- [ ] **Step 4: deploy.md**

`panel/deploy.md`:
````markdown
# Деплой xray-panel

## Сборка (на машине разработки)
```bash
cd panel && make build      # -> ./xray-panel (linux/amd64)
```

## Установка на VPS (root)
```bash
scp xray-panel root@SERVER:/usr/local/bin/xray-panel
scp panel.env.example root@SERVER:/usr/local/etc/xray/panel.env   # отредактируйте
scp enable_stats.sh root@SERVER:/usr/local/etc/xray/autoXRAY-multi/
scp xray-panel.service root@SERVER:/etc/systemd/system/
```

## Один раз: включить статистику и применить email клиентам
```bash
AX_DIR=/usr/local/etc/xray bash /usr/local/etc/xray/autoXRAY-multi/enable_stats.sh
systemctl restart xray
/usr/local/etc/xray/autoXRAY-multi/update_clients.sh   # перезапишет clients с email
```

## nginx: location за basic auth
```nginx
location /admin/ {
    auth_basic "panel";
    auth_basic_user_file /etc/nginx/.htpasswd;
    proxy_pass http://127.0.0.1:8088/;
    proxy_set_header Host $host;
}
```
```bash
apt-get install -y apache2-utils
htpasswd -c /etc/nginx/.htpasswd admin
nginx -t && systemctl reload nginx
```

## Запуск панели
```bash
systemctl daemon-reload
systemctl enable --now xray-panel
systemctl status xray-panel
```

## Ручной чеклист проверки (на сервере)
1. `curl -s 127.0.0.1:8088/api/users | jq` — список клиентов с трафиком.
2. Открыть `https://ДОМЕН/admin/` (basic auth) — таблица рендерится.
3. Добавить юзера через "+ Добавить" → появился в `clients.txt`, создан `clients/<имя>.env`, страница `https://ДОМЕН/<имя>.html` доступна.
4. Прогнать трафик через нового юзера → через ~30с в панели растут up/down.
5. Удалить юзера → исчез из таблицы и `clients.txt`, `xray -test` прошёл, xray перезапущен.
6. `xray api statsquery --server=127.0.0.1:10085 -pattern "user>>>"` — счётчики по email.
````

- [ ] **Step 5: Commit**

```bash
chmod +x panel/enable_stats.sh
git add panel/xray-panel.service panel/panel.env.example panel/Makefile panel/deploy.md
git commit -m "chore(panel): deploy artifacts (systemd, env, makefile, docs)"
```

---

## Task 14: Full test pass + README

**Files:**
- Modify: `autoXRAY-multi/README.md` (add panel section) — or create `panel/README.md`

- [ ] **Step 1: Run the whole Go test suite**

Run: `cd panel && go test ./...`
Expected: all PASS (runner/api bash-dependent tests skip on Windows; run on Linux for full coverage).

- [ ] **Step 2: go vet**

Run: `cd panel && go vet ./...`
Expected: no findings.

- [ ] **Step 3: Add panel README**

`panel/README.md`:
```markdown
# xray-panel

Минималистичная админ-панель для autoXRAY-multi: добавление/удаление клиентов
и просмотр реального расхода трафика (xray Stats API).

- Стек: Go (один бинарь, фронт вшит), nginx basic auth, systemd.
- Модель: переиспользует `clients.txt` + `update_clients.sh` из autoXRAY-multi.
- Деплой и проверка: см. [deploy.md](deploy.md).
- Дизайн/архитектура: см. `docs/superpowers/specs/2026-06-19-xray-admin-panel-design.md`.
```

- [ ] **Step 4: Commit**

```bash
git add panel/README.md
git commit -m "docs(panel): readme"
```

---

## Self-Review Notes

- **Spec coverage:** Go binary+embed (T1,9), clients model+add/delete (T2,3), runner (T4), stats accumulator+query (T5,6), node load (T7), REST API incl. rollback & mutex (T8), poller (T9), frontend pixel-match (T10), email tagging (T11), enable_stats.sh (T12), systemd/nginx/deploy (T13), tests+docs (T14). All spec sections mapped.
- **Type consistency:** `Client{Name,UUID,SubPath}`, `Snapshot{Up,Down}`, `Totals{Up,Down,LastDelta,LastSeen}` used identically across stats/api tasks. `Store.Apply/Get/Load`, `clients.Parse/Add/Delete/ValidateName/AllowedNames`, `runner.Run`, `stats.Query/parseStatsJSON`, `node.Load/parseLoadAvg`, `api.Server.Handler/RunFunc` consistent.
- **Known limitation carried from spec:** 7-day per-user chart is synthetic until daily snapshot history accumulates; location/expiry shown as "—" pending model fields. Documented in spec, not a plan gap.
