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
