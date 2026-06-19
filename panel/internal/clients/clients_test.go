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
