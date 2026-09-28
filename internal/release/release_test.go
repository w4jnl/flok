package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

func TestTargetsAndVersions(t *testing.T) {
	for in, want := range map[string]string{"Darwin arm64": "darwin/arm64", "Linux x86_64": "linux/amd64", "Linux aarch64": "linux/arm64", "Linux arm64": "linux/arm64", "linux amd64": "linux/amd64"} {
		got, err := TargetFromUname(in)
		if err != nil || got.String() != want {
			t.Fatalf("%q: %v %v, want %s", in, got, err, want)
		}
	}
	for _, in := range []string{"Linux armv7l", "FreeBSD amd64", "Linux", ""} {
		if _, err := TargetFromUname(in); err == nil {
			t.Fatalf("%q must not map to a release", in)
		}
	}
	local := Target{runtime.GOOS, runtime.GOARCH}
	if !local.Local() || (Target{"plan9", "mips"}).Local() {
		t.Fatal("Local")
	}
	for v, want := range map[string]bool{"v0.5.0": true, "0.5.0": true, "0.5.0-1-gbfb2733": false, "v0.5.0-dirty": false, "HEAD-bfb2733": false, "dev": false, "bfb2733": false} {
		if IsRelease(v) != want {
			t.Fatalf("IsRelease(%q) = %v", v, !want)
		}
	}
	if AssetName("v0.5.0", Target{"linux", "arm64"}) != "flok_0.5.0_linux_arm64.tar.gz" || Member("0.5.0", Target{"darwin", "arm64"}) != "flok_0.5.0_darwin_arm64/flok" {
		t.Fatal("names")
	}
	sums, err := ParseSums([]byte("aa" + strings.Repeat("0", 62) + "  flok_0.5.0_linux_amd64.tar.gz\n" + strings.Repeat("b", 64) + " *flok_0.5.0_linux_arm64.tar.gz\n\n"))
	if err != nil || len(sums) != 2 || sums["flok_0.5.0_linux_arm64.tar.gz"] != strings.Repeat("b", 64) {
		t.Fatalf("sums %v %v", sums, err)
	}
	if v, err := VersionFromSums(sums); err != nil || v != "0.5.0" {
		t.Fatalf("version %q %v", v, err)
	}
	if _, err := ParseSums([]byte("not a sums file\n")); err == nil {
		t.Fatal("garbage sums accepted")
	}
	if _, err := VersionFromSums(map[string]string{"README": "x"}); err == nil {
		t.Fatal("no tarball, no version")
	}
}

// tarball builds a release archive with one member.
func tarball(t *testing.T, member, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: member, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func TestResolveAndBinary(t *testing.T) {
	amd := tarball(t, "flok_0.5.0_linux_amd64/flok", "#!/bin/sh\necho flok 0.5.0\n")
	arm := tarball(t, "flok_0.5.0_linux_arm64/flok", "#!/bin/sh\necho flok 0.5.0 arm\n")
	bad := tarball(t, "flok_0.4.6_linux_amd64/README", "no binary here")
	sums050 := fmt.Sprintf("%s  flok_0.5.0_linux_amd64.tar.gz\n%s  flok_0.5.0_linux_arm64.tar.gz\n%s  flok_0.5.0_darwin_arm64.tar.gz\n", sha(amd), sha(arm), strings.Repeat("0", 64))
	sums046 := fmt.Sprintf("%s  flok_0.4.6_linux_amd64.tar.gz\n", sha(bad))
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		switch r.URL.Path {
		case "/releases/latest/download/sha256sums.txt":
			http.Redirect(w, r, "/releases/download/v0.5.0/sha256sums.txt", http.StatusFound)
		case "/releases/download/v0.5.0/sha256sums.txt":
			_, _ = w.Write([]byte(sums050))
		case "/releases/download/v0.5.0/flok_0.5.0_linux_amd64.tar.gz":
			_, _ = w.Write(amd)
		case "/releases/download/v0.5.0/flok_0.5.0_linux_arm64.tar.gz":
			_, _ = w.Write(arm[:len(arm)-3]) // truncated: the sum will not match
		case "/releases/download/v0.4.6/sha256sums.txt":
			_, _ = w.Write([]byte(sums046))
		case "/releases/download/v0.4.6/flok_0.4.6_linux_amd64.tar.gz":
			_, _ = w.Write(bad)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	BaseURL = srv.URL + "/releases"
	ctx := context.Background()

	latest, err := Resolve(ctx, "")
	if err != nil || latest.Version != "0.5.0" || len(latest.Sums) != 3 {
		t.Fatalf("latest: %+v %v", latest, err)
	}
	var progressed bool
	bin, err := latest.Binary(ctx, Target{"linux", "amd64"}, func(done, total int64) { progressed = done > 0 })
	if err != nil || !strings.Contains(string(bin), "echo flok 0.5.0") || !progressed {
		t.Fatalf("binary: %q %v progressed=%v", bin, err, progressed)
	}
	if _, err := latest.Binary(ctx, Target{"linux", "arm64"}, nil); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("a corrupted download must fail on the checksum: %v", err)
	}
	if _, err := latest.Binary(ctx, Target{"darwin", "arm64"}, nil); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("a missing asset names the status: %v", err)
	}
	if _, err := latest.Binary(ctx, Target{"darwin", "amd64"}, nil); err == nil || !strings.Contains(err.Error(), "no flok build for darwin/amd64") {
		t.Fatalf("a target the release lacks: %v", err)
	}
	pinned, err := Resolve(ctx, "v0.4.6")
	if err != nil || pinned.Version != "0.4.6" {
		t.Fatalf("pinned: %+v %v", pinned, err)
	}
	if _, err := pinned.Binary(ctx, Target{"linux", "amd64"}, nil); err == nil || !strings.Contains(err.Error(), "has no flok_0.4.6_linux_amd64/flok") {
		t.Fatalf("a tarball without the binary: %v", err)
	}
	if _, err := Resolve(ctx, "9.9.9"); err == nil || !strings.Contains(err.Error(), "release 9.9.9") {
		t.Fatalf("unknown release: %v", err)
	}
	if !strings.HasSuffix(hits[0], "/latest/download/sha256sums.txt") || !strings.HasSuffix(hits[2], "/v0.5.0/flok_0.5.0_linux_amd64.tar.gz") {
		t.Fatalf("latest goes through the redirect, then the pinned asset: %v", hits)
	}
}
