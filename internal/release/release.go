// Package release knows flok's GitHub releases: which tarball a host needs, where it lives, and
// how to check and unpack it. It is transport-independent; `flok host install` (remote.Install)
// streams what it returns over ssh. Everything is standard library: net/http for the download,
// crypto/sha256 against the release's sha256sums.txt, archive/tar and compress/gzip in memory.
package release

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// BaseURL is the releases root; tests point it at an httptest server. "latest" resolves through
// GitHub's redirect of <BaseURL>/latest/download/<file> to the pinned release, so no API call and
// no rate limit are involved.
var BaseURL = "https://github.com/w4jnl/flok/releases"

// Client fetches the assets; the default transport honours HTTPS_PROXY and friends.
var Client = &http.Client{Timeout: 5 * time.Minute}

// Target is the OS and CPU a build is for, as Go names them.
type Target struct{ OS, Arch string }

// TargetFromUname maps `uname -sm` output to a release target.
func TargetFromUname(sm string) (Target, error) {
	f := strings.Fields(sm)
	if len(f) < 2 {
		return Target{}, fmt.Errorf("cannot tell the host's OS and CPU from %q", sm)
	}
	unsupported := fmt.Errorf("no flok release for %s; build it there or use --from", strings.Join(f, " "))
	var t Target
	switch strings.ToLower(f[0]) {
	case "darwin":
		t.OS = "darwin"
	case "linux":
		t.OS = "linux"
	default:
		return Target{}, unsupported
	}
	switch strings.ToLower(f[1]) {
	case "x86_64", "amd64":
		t.Arch = "amd64"
	case "arm64", "aarch64":
		t.Arch = "arm64"
	default:
		return Target{}, unsupported
	}
	return t, nil
}

// Local says whether this binary would run there.
func (t Target) Local() bool { return t.OS == runtime.GOOS && t.Arch == runtime.GOARCH }

func (t Target) String() string { return t.OS + "/" + t.Arch }

var releaseRe = regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)

// IsRelease says whether a flok version string names a release (v0.5.0, 0.5.0), as opposed to a
// build between releases (0.5.0-1-gbfb2733, HEAD-bfb2733, dev).
func IsRelease(v string) bool { return releaseRe.MatchString(v) }

// Clean drops the leading v.
func Clean(v string) string { return strings.TrimPrefix(v, "v") }

// AssetName is the tarball a release ships for a target.
func AssetName(ver string, t Target) string {
	return "flok_" + Clean(ver) + "_" + t.OS + "_" + t.Arch + ".tar.gz"
}

// Member is the path of the binary inside that tarball.
func Member(ver string, t Target) string {
	return strings.TrimSuffix(AssetName(ver, t), ".tar.gz") + "/flok"
}

// ParseSums reads a sha256sums.txt (GNU format: "<hex>  <name>", a leading * for binary mode).
func ParseSums(data []byte) (map[string]string, error) {
	sums := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 || len(f[0]) != 64 {
			return nil, fmt.Errorf("sha256sums.txt: unexpected line %q", line)
		}
		sums[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
	}
	if len(sums) == 0 {
		return nil, errors.New("sha256sums.txt is empty")
	}
	return sums, nil
}

var assetRe = regexp.MustCompile(`^flok_(\d+\.\d+\.\d+)_[a-z0-9]+_[a-z0-9]+\.tar\.gz$`)

// VersionFromSums reads the release version off the tarball names in its sha256sums.txt.
func VersionFromSums(sums map[string]string) (string, error) {
	for name := range sums {
		if m := assetRe.FindStringSubmatch(name); m != nil {
			return m[1], nil
		}
	}
	return "", errors.New("sha256sums.txt names no flok tarball")
}

// Source is one release: its version and the checksums of its assets.
type Source struct {
	Version string
	Sums    map[string]string
}

// Resolve picks a release: want "" is the latest, else that version (v optional). The sums file
// is fetched first, so the tarball is always taken from the release the sums describe.
func Resolve(ctx context.Context, want string) (Source, error) {
	url := BaseURL + "/latest/download/sha256sums.txt"
	label := "the latest release"
	if want != "" {
		url = BaseURL + "/download/v" + Clean(want) + "/sha256sums.txt"
		label = "release " + Clean(want)
	}
	data, err := get(ctx, url, 1<<20, nil)
	if err != nil {
		return Source{}, fmt.Errorf("%s: %w", label, err)
	}
	sums, err := ParseSums(data)
	if err != nil {
		return Source{}, fmt.Errorf("%s: %w", label, err)
	}
	ver := Clean(want)
	if ver == "" {
		if ver, err = VersionFromSums(sums); err != nil {
			return Source{}, fmt.Errorf("%s: %w", label, err)
		}
	}
	return Source{Version: ver, Sums: sums}, nil
}

// maxAsset bounds a download and the unpacked binary.
const maxAsset = 200 << 20

// Binary downloads the tarball for t, checks it against the sums and returns the flok binary
// inside it. progress, when set, sees the bytes received so far and the total (-1 when unknown).
func (s Source) Binary(ctx context.Context, t Target, progress func(done, total int64)) ([]byte, error) {
	name := AssetName(s.Version, t)
	want, ok := s.Sums[name]
	if !ok {
		return nil, fmt.Errorf("release %s has no %s: no flok build for %s", s.Version, name, t)
	}
	data, err := get(ctx, BaseURL+"/download/v"+s.Version+"/"+name, maxAsset, progress)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return nil, fmt.Errorf("%s: sha256 %s, but sha256sums.txt says %s", name, got, want)
	}
	bin, err := extract(data, Member(s.Version, t))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return bin, nil
}

func get(ctx context.Context, url string, limit int64, progress func(done, total int64)) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	var buf bytes.Buffer
	r := io.LimitReader(resp.Body, limit+1)
	chunk := make([]byte, 64<<10)
	for {
		n, err := r.Read(chunk)
		buf.Write(chunk[:n])
		if progress != nil && n > 0 {
			progress(int64(buf.Len()), resp.ContentLength)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if int64(buf.Len()) > limit {
		return nil, fmt.Errorf("GET %s: larger than %d MB", url, limit>>20)
	}
	return buf.Bytes(), nil
}

// extract returns one regular file out of a gzipped tarball.
func extract(tgz []byte, member string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, fmt.Errorf("not a gzipped tarball: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Name != member && h.Name != "./"+member {
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("%s in the tarball is not a regular file", member)
		}
		b, err := io.ReadAll(io.LimitReader(tr, maxAsset))
		if err != nil {
			return nil, err
		}
		return b, nil
	}
	return nil, fmt.Errorf("the tarball has no %s", member)
}
