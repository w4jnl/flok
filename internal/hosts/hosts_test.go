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
		"upper name":     {Name: "Beta", Target: "beta", Mode: ModeFull},
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
