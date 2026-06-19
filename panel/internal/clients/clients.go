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
