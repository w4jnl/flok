package cli

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCompletionScriptsParse(t *testing.T) {
	for shell, script := range map[string]string{"bash": bashCompletion(), "zsh": zshCompletion()} {
		if _, err := exec.LookPath(shell); err != nil {
			t.Logf("%s not installed, skipping syntax check", shell)
			continue
		}
		cmd := exec.Command(shell, "-n")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s -n failed: %v\n%s", shell, err, out)
		}
	}
}

func TestBashCompletionCompletesCommands(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	probe := bashCompletion() + `
COMP_WORDS=(flok st); COMP_CWORD=1; _flok; echo "${COMPREPLY[*]}"
COMP_WORDS=(flok install --c); COMP_CWORD=2; _flok; echo "${COMPREPLY[*]}"
`
	out, err := exec.Command("bash", "-c", probe).CombinedOutput()
	if err != nil {
		t.Fatalf("bash: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 || lines[0] != "status" || !strings.Contains(lines[1], "--claude") || !strings.Contains(lines[1], "--copilot") {
		t.Fatalf("unexpected completions: %q", lines)
	}
	for _, c := range commands {
		if !strings.Contains(bashCompletion(), c.name) || !strings.Contains(zshCompletion(), "'"+c.name+":") {
			t.Errorf("command %s missing from a script", c.name)
		}
	}
}
