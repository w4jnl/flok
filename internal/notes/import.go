package notes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Timeout is how long an import may take; the hook never waits longer.
const Timeout = 2 * time.Second

// Resolve turns [notes] command into the nt to run: "" for off, else an absolute path.
// "auto" takes the nt on PATH; a path is taken as given (~ expanded) when it is there.
func Resolve(command string) string {
	switch c := strings.TrimSpace(command); c {
	case "", "off", "false", "no":
		return ""
	case "auto", "on", "true", "nt":
		p, err := exec.LookPath("nt")
		if err != nil {
			return ""
		}
		return p
	default:
		if strings.HasPrefix(c, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				c = filepath.Join(home, c[2:])
			}
		}
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
		return ""
	}
}

// Import hands rec to nt. A non-zero exit is an error with nt's one line.
func Import(nt string, rec Record) error {
	if nt == "" {
		return errors.New("no nt")
	}
	body, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, nt, "agent", "import")
	cmd.Stdin = bytes.NewReader(body)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("nt agent import: no answer in %s", Timeout)
		}
		line := strings.TrimSpace(stderr.String())
		if i := strings.IndexByte(line, '\n'); i >= 0 {
			line = line[:i]
		}
		if line == "" {
			line = err.Error()
		}
		return fmt.Errorf("nt agent import: %s", line)
	}
	return nil
}

// Probe asks nt which record version it takes (`nt agent import --probe`); 0 with an error
// when it has no import (too old) or does not run.
func Probe(nt string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, nt, "agent", "import", "--probe").Output()
	if err != nil {
		return 0, fmt.Errorf("%s has no agent import (nt 0.7.0 or newer)", nt)
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("%s agent import --probe: %q", nt, strings.TrimSpace(string(out)))
	}
	return v, nil
}
