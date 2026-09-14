package notify

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBundledAndResolve(t *testing.T) {
	dir := t.TempDir()
	files := BundledFiles(dir)
	for _, kind := range []string{"done", "blocked", "error"} {
		fi, err := os.Stat(files[kind])
		if err != nil || fi.Size() < 1000 {
			t.Fatalf("%s: bundled sound not written: %v", kind, err)
		}
	}
	if filepath.Base(files["blocked"]) != "request.wav" || filepath.Base(files["done"]) != "done.wav" {
		t.Fatalf("unexpected mapping: %v", files)
	}
	r := Resolve(dir, map[string]string{"done": "/x/custom.aiff", "blocked": ""})
	if r["done"] != "/x/custom.aiff" || r["blocked"] != files["blocked"] || r["error"] != files["error"] {
		t.Fatalf("resolve: %v", r)
	}
}
