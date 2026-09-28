package hosts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	ok := Host{Name: "beta", Target: "jaro@beta.example.org", Mode: ModeFull}
	if err := Validate(ok); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]Host{
		"local name":     {Name: "local", Target: "beta", Mode: ModeFull},
		"Local name":     {Name: "Local", Target: "beta", Mode: ModeFull},
		"dotted name":    {Name: "docker.ams", Target: "beta", Mode: ModeFull},
		"leading dash":   {Name: "-x", Target: "beta", Mode: ModeFull},
		"long name":      {Name: strings.Repeat("a", 33), Target: "beta", Mode: ModeFull},
		"target option":  {Name: "beta", Target: "-oProxyCommand=x", Mode: ModeFull},
		"target space":   {Name: "beta", Target: "beta x", Mode: ModeFull},
		"mode":           {Name: "beta", Target: "beta", Mode: "ssh"},
		"socket slash":   {Name: "beta", Target: "beta", Mode: ModePlain, Socket: "../x"},
		"flok relative":  {Name: "beta", Target: "beta", Mode: ModeFull, Flok: "bin/flok"},
		"flok semicolon": {Name: "beta", Target: "beta", Mode: ModeFull, Flok: "/bin/flok;rm"},
		"session colon":  {Name: "beta", Target: "beta", Mode: ModeFull, Session: "a:b"},
		"term space":     {Name: "beta", Target: "beta", Mode: ModeFull, Term: "x y"},
	} {
		if err := Validate(bad); err == nil {
			t.Errorf("%s: %+v must be rejected", name, bad)
		}
	}
	if err := Validate(Host{Name: "dockerAMS", Target: "dockerAMS", Mode: ModeFull}); err != nil {
		t.Errorf("a mixed-case name (an ssh alias) is valid: %v", err)
	}
	if err := ValidateName("docker.ams"); err == nil || !strings.Contains(err.Error(), "(e.g. docker-ams)") {
		t.Errorf("an invalid name suggests a valid one: %v", err)
	}
	for _, target := range []string{"beta", "10.0.0.2", "user@host", "user@2001:db8::1", "jump-host_1"} {
		if err := Validate(Host{Name: "b", Target: target, Mode: ModePlain, Socket: "default", Flok: "/opt/homebrew/bin/flok", Session: "work-1", Term: "screen-256color"}); err != nil {
			t.Errorf("%q: %v", target, err)
		}
	}
}

func TestLoadSaveUpdateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil || s.Version != Version || len(s.Hosts) != 0 {
		t.Fatalf("missing file: %+v %v", s, err)
	}
	at := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	s, err = Update(dir, func(s *Set) error {
		return s.Add(Host{Name: "beta", Target: "beta", Mode: ModeFull, Enabled: true, AddedAt: at})
	})
	if err != nil || len(s.Hosts) != 1 {
		t.Fatalf("add: %+v %v", s, err)
	}
	if _, err := Update(dir, func(s *Set) error { return s.Add(Host{Name: "beta", Target: "x", Mode: ModeFull}) }); err == nil {
		t.Fatal("a duplicate name must be refused")
	}
	raw, _ := os.ReadFile(Path(dir))
	if strings.Contains(string(raw), "last_connected") || strings.Contains(string(raw), `"socket"`) || !strings.Contains(string(raw), `"version": 1`) {
		t.Fatalf("empty optional fields must be omitted:\n%s", raw)
	}
	got, err := Load(dir)
	if err != nil || len(got.Hosts) != 1 || got.Hosts[0].Name != "beta" || !got.Hosts[0].AddedAt.Equal(at) || !got.Hosts[0].Enabled {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	if h, ok := got.Get("beta"); !ok || h.Mode != ModeFull {
		t.Fatalf("get: %+v %v", h, ok)
	}
	if !got.SetEnabled("beta", false) || got.SetEnabled("nope", true) || len(got.Enabled()) != 0 {
		t.Fatal("SetEnabled")
	}
	if err := got.Set("beta", func(h *Host) { h.Mode = ModePlain; h.Socket = "agents"; h.Name = "renamed" }); err != nil || got.Hosts[0].Mode != ModePlain || got.Hosts[0].Name != "beta" {
		t.Fatalf("Set edits in place and keeps the name: %v %+v", err, got.Hosts[0])
	}
	if got.Set("beta", func(h *Host) { h.Target = "-x" }) == nil || got.Hosts[0].Target != "beta" {
		t.Fatal("an invalid edit is refused and leaves the host as it was")
	}
	if got.Set("nope", func(*Host) {}) == nil {
		t.Fatal("Set on a missing host")
	}
	if !AttachChanged(Host{Target: "a"}, Host{Target: "b"}) || AttachChanged(Host{Mode: ModeFull}, Host{Mode: ModePlain}) {
		t.Fatal("AttachChanged")
	}
	if !got.Remove("beta") || got.Remove("beta") || len(got.Names()) != 0 {
		t.Fatal("Remove")
	}
	// a newer file is refused, not silently rewritten
	_ = os.WriteFile(Path(dir), []byte(`{"version":2,"hosts":[]}`), 0o644)
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("newer version: %v", err)
	}
	_ = os.WriteFile(Path(dir), []byte(`{"version":1,"hosts":[`), 0o644)
	if _, err := Load(dir); err == nil {
		t.Fatal("corrupt JSON must be an error")
	}
}

// Concurrent Updates serialize on the lock: no add is lost.
func TestUpdateSerializes(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "h" + string(rune('a'+i))
			if _, err := Update(dir, func(s *Set) error { return s.Add(Host{Name: name, Target: name, Mode: ModePlain, Enabled: true}) }); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	got, err := Load(dir)
	if err != nil || len(got.Hosts) != 8 {
		t.Fatalf("want 8 hosts, got %d (%v)", len(got.Hosts), err)
	}
	var check Set
	data, _ := os.ReadFile(filepath.Join(dir, File))
	if json.Unmarshal(data, &check) != nil {
		t.Fatal("the file must always be complete JSON")
	}
}

func TestNamesMatchInAnyCase(t *testing.T) {
	var s Set
	if err := s.Add(Host{Name: "dockerAMS", Target: "dockerAMS", Mode: ModeFull, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// one host per name regardless of case: the per-host dirs share a case-insensitive filesystem on macOS
	if err := s.Add(Host{Name: "dockerams", Target: "x", Mode: ModeFull}); err == nil || !strings.Contains(err.Error(), `"dockerAMS" exists`) {
		t.Fatalf("a case-only duplicate must be refused with the registered spelling: %v", err)
	}
	if h, ok := s.Get("DOCKERams"); !ok || h.Name != "dockerAMS" {
		t.Fatalf("Get ignores case and returns the registered spelling: %+v %v", h, ok)
	}
	if !s.SetEnabled("dockerams", false) || s.Hosts[0].Enabled {
		t.Fatalf("SetEnabled ignores case: %+v", s.Hosts[0])
	}
	if err := s.Set("DockerAms", func(h *Host) { h.Session, h.Name = "work", "renamed" }); err != nil {
		t.Fatal(err)
	}
	if h := s.Hosts[0]; h.Name != "dockerAMS" || h.Session != "work" || h.Target != "dockerAMS" {
		t.Fatalf("Set keeps the name as registered and the target as typed: %+v", h)
	}
	if !s.Remove("DOCKERAMS") || len(s.Hosts) != 0 {
		t.Fatalf("Remove ignores case: %+v", s.Hosts)
	}
}

func TestNameFromTarget(t *testing.T) {
	for target, want := range map[string]string{
		"dockerAMS":             "dockerAMS", // an ssh alias as typed
		"jaro@beta.example.org": "beta",
		"beta:2222":             "beta",
		"user@gpu-1.lab:22":     "gpu-1",
		"10.0.0.5":              "10-0-0-5",
		"root@10.0.0.5:2222":    "10-0-0-5",
		"jump-host_1":           "jump-host_1",
		"-x.example.org":        "x",
		strings.Repeat("a", 40): strings.Repeat("a", 32),
	} {
		if got, err := NameFromTarget(target); err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", target, got, err, want)
		} else if err := ValidateName(got); err != nil {
			t.Errorf("%q: derived %q is invalid: %v", target, got, err)
		}
	}
	for _, target := range []string{"user@2001:db8::1", "::1", "local", "user@LOCAL.example.org", "@", "..."} {
		if got, err := NameFromTarget(target); err == nil {
			t.Errorf("%q: want an error asking for a name, got %q", target, got)
		}
	}
}
