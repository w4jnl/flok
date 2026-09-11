package git

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBranchRepoWorktreeAndDetached(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git", "worktrees", "wt"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
	sub := filepath.Join(repo, "a", "b")
	os.MkdirAll(sub, 0o755)
	if got := lookup(sub); got != "main" {
		t.Fatalf("branch = %q, want main", got)
	}
	wt := filepath.Join(root, "wt")
	os.MkdirAll(wt, 0o755)
	os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+filepath.Join(repo, ".git", "worktrees", "wt")+"\n"), 0o644)
	os.WriteFile(filepath.Join(repo, ".git", "worktrees", "wt", "HEAD"), []byte("ref: refs/heads/feature/x\n"), 0o644)
	if got := lookup(wt); got != "feature/x" {
		t.Fatalf("worktree branch = %q", got)
	}
	os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("0123456789abcdef\n"), 0o644)
	if got := lookup(repo); got != "0123456" {
		t.Fatalf("detached = %q", got)
	}
	if got := lookup(root); got != "" {
		t.Fatalf("outside repo = %q", got)
	}
}
