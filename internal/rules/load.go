package rules

import (
	"embed"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

//go:embed manifests/*.toml
var bundled embed.FS

// Set is the loaded manifests keyed by agent id.
type Set struct {
	mu        sync.Mutex
	manifests map[string]*Manifest
	Problems  []string
}

// Load reads bundled manifests, then herdr's cache (optional), then the user's override dir;
// each later source replaces a manifest of the same id wholesale.
func Load(userDir string, herdrCache bool) *Set {
	s := &Set{manifests: map[string]*Manifest{}}
	entries, _ := bundled.ReadDir("manifests")
	for _, e := range entries {
		data, err := bundled.ReadFile("manifests/" + e.Name())
		if err == nil {
			s.add(string(data), "bundled "+e.Name())
		}
	}
	if herdrCache {
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".local", "state", "herdr", "agent-detection", "remote")
		if v := os.Getenv("XDG_STATE_HOME"); v != "" {
			dir = filepath.Join(v, "herdr", "agent-detection", "remote")
		}
		s.addDir(dir, "herdr cache")
	}
	if userDir != "" {
		s.addDir(userDir, "override")
	}
	return s
}

func (s *Set) addDir(dir, label string) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.toml"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		s.add(string(data), label+" "+filepath.Base(f))
	}
}

func (s *Set) add(text, label string) {
	m, err := Parse(text)
	if err != nil {
		s.Problems = append(s.Problems, label+": "+err.Error())
		return
	}
	for _, sk := range m.Skipped {
		s.Problems = append(s.Problems, label+": skipped rule "+sk)
	}
	s.mu.Lock()
	s.manifests[m.ID] = m
	for _, a := range m.Aliases {
		if _, taken := s.manifests[a]; !taken {
			s.manifests[a] = m
		}
	}
	s.mu.Unlock()
}

// Get returns the manifest for an agent id (or alias), or nil.
func (s *Set) Get(id string) *Manifest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.manifests[strings.ToLower(id)]
}

// IDs lists loaded manifest ids.
func (s *Set) IDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[*Manifest]bool{}
	var out []string
	for id, m := range s.manifests {
		if id == m.ID && !seen[m] {
			seen[m] = true
			out = append(out, id)
		}
	}
	return out
}
