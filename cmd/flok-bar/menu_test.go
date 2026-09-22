//go:build darwin

package main

import (
	"strings"
	"testing"
)

func TestVersionRowLinks(t *testing.T) {
	if !strings.HasPrefix(changelogURL, repoURL+"/") || !strings.HasSuffix(changelogURL, "/CHANGELOG.md") {
		t.Fatalf("changelog link %q is not the repository's CHANGELOG.md", changelogURL)
	}
}
