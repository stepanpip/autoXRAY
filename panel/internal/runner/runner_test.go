package runner

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

func TestRunTimesOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs bash")
	}
	_, err := run(stub(t, `sleep 5`), nil, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error should mention timeout: %v", err)
	}
}
