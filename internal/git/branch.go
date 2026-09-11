// Package git answers "which branch is this path on" by reading .git/HEAD directly (no exec),
// with worktree support and a short cache so the sidebar can ask every second.
package git

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type entry struct {
	branch string
	at     time.Time
}

var (
	mu    sync.Mutex
	cache = map[string]entry{}
	ttl   = 5 * time.Second
)

// Branch returns the branch name for the repository containing path, a short commit id when
// detached, or "" when path is not inside a repository.
func Branch(path string) string {
	if path == "" {
		return ""
	}
	mu.Lock()
	e, ok := cache[path]
	mu.Unlock()
	if ok && time.Since(e.at) < ttl {
		return e.branch
	}
	b := lookup(path)
	mu.Lock()
	cache[path] = entry{b, time.Now()}
	mu.Unlock()
	return b
}

func lookup(path string) string {
	dir := path
	for i := 0; i < 64; i++ {
		gitPath := filepath.Join(dir, ".git")
		if fi, err := os.Stat(gitPath); err == nil {
			if fi.IsDir() {
				return readHead(gitPath)
			}
			data, err := os.ReadFile(gitPath)
			if err != nil {
				return ""
			}
			line := strings.TrimSpace(string(data))
			if strings.HasPrefix(line, "gitdir: ") {
				gd := strings.TrimPrefix(line, "gitdir: ")
				if !filepath.IsAbs(gd) {
					gd = filepath.Join(dir, gd)
				}
				return readHead(gd)
			}
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}

func readHead(gitDir string) string {
	data, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(data))
	if strings.HasPrefix(line, "ref: ") {
		return strings.TrimPrefix(strings.TrimPrefix(line, "ref: "), "refs/heads/")
	}
	if len(line) > 7 {
		return line[:7]
	}
	return line
}
