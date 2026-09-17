package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/w4jnl/flok/internal/resurrect"
)

func TestWriteResurrectDiagnosticsReplacesAndClears(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("FLOK_STATE", stateDir)
	path := filepath.Join(stateDir, "resurrect.log")

	first := []resurrect.Skipped{{PaneID: "%1", Kind: "copilot", Reason: "no hook record"}}
	if err := writeResurrectDiagnostics(first); err != nil {
		t.Fatal(err)
	}
	second := []resurrect.Skipped{{PaneID: "%2", Kind: "claude", Reason: "no usable session ID"}}
	if err := writeResurrectDiagnostics(second); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, "%1") || !strings.Contains(got, "%2 claude: no usable session ID") {
		t.Fatalf("diagnostics must contain only the latest save: %q", got)
	}

	if err := writeResurrectDiagnostics(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("diagnostics should be removed when no panes are skipped: %v", err)
	}
}
