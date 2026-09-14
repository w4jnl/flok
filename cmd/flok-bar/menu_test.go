//go:build darwin

package main

import "testing"

func TestReleaseNotesURL(t *testing.T) {
	if got := releaseNotesURL("v0.3.4"); got != "https://github.com/w4jnl/flok/releases/tag/v0.3.4" {
		t.Fatalf("tagged build: %s", got)
	}
	if got := releaseNotesURL("0.3.4-1-gcc72d7c-dirty"); got != "https://github.com/w4jnl/flok/releases" {
		t.Fatalf("dev build: %s", got)
	}
}
