package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// Timeout caps a single script run so a hung update (e.g. xray reload stalling)
// cannot block the API handler — and the mutex it holds — indefinitely.
const Timeout = 60 * time.Second

// Run executes script via bash, with extra env merged over the process env.
// Returns combined output; on non-zero exit returns an error wrapping output.
// The run is aborted after Timeout.
func Run(script string, env map[string]string) (string, error) {
	return run(script, env, Timeout)
}

func run(script string, env map[string]string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/bash", script)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("%s timed out after %s\n%s", script, timeout, out)
	}
	if err != nil {
		return out, fmt.Errorf("%s failed: %v\n%s", script, err, out)
	}
	return out, nil
}
