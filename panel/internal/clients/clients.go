package clients

import (
	"bufio"
	"fmt"
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
