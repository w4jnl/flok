package tunnel

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// pair runs a tunnel server on an httptest WebSocket and dials it with pin; the server side
// echoes every frame back upper-cased.
func pair(t *testing.T, cert tls.Certificate, pin string) (*Conn, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		c, err := Server(r.Context(), ws, cert, 1<<20)
		if err != nil {
			return
		}
		for {
			f, err := c.Read(r.Context())
			if err != nil {
				return
			}
			if err := c.Write(r.Context(), bytes.ToUpper(f)); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.CloseNow() })
	return Client(ctx, ws, pin, 1<<20)
}

func TestKeyIsCreatedOnceAndPinned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "link-key.pem")
	a, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v, want 0600", st.Mode(), err)
	}
	b, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	pa, _ := PinOf(a)
	pb, _ := PinOf(b)
	if pa != pb || !ValidPin(pa) {
		t.Fatalf("pins %q %q: the same key must load again with the same pin", pa, pb)
	}
	for _, bad := range []string{"", "sha256:", "sha1:" + pa[7:], pa + "x", strings.TrimPrefix(pa, "sha256:")} {
		if ValidPin(bad) {
			t.Errorf("ValidPin(%q) = true", bad)
		}
	}
}

func TestFramesGoBothWaysUnderThePin(t *testing.T) {
	cert, _ := LoadOrCreate(filepath.Join(t.TempDir(), "k.pem"))
	pin, _ := PinOf(cert)
	c, err := pair(t, cert, pin)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	big := strings.Repeat("x", 200<<10) // spans many TLS records and reader buffers
	for _, f := range []string{`{"type":"hello"}`, big} {
		if err := c.Write(ctx, []byte(f)); err != nil {
			t.Fatal(err)
		}
		got, err := c.Read(ctx)
		if err != nil || string(got) != strings.ToUpper(f) {
			t.Fatalf("echo of %d bytes: %d bytes, %v", len(f), len(got), err)
		}
	}
	if err := c.Write(ctx, []byte("a\nb")); err == nil {
		t.Error("a frame with a newline was written")
	}
}

func TestAnotherKeyIsRefused(t *testing.T) {
	dir := t.TempDir()
	cert, _ := LoadOrCreate(filepath.Join(dir, "relay.pem"))
	other, _ := LoadOrCreate(filepath.Join(dir, "proxy.pem")) // what an inspecting proxy would answer with
	pin, _ := PinOf(other)
	_, err := pair(t, cert, pin)
	if !errors.Is(err, ErrPinMismatch) {
		t.Fatalf("err = %v, want ErrPinMismatch", err)
	}
	real, _ := PinOf(cert)
	if !strings.Contains(err.Error(), real) {
		t.Errorf("err %q should name the key that answered (%s)", err, real)
	}
}

func TestReadStopsAtTheLimit(t *testing.T) {
	cert, _ := LoadOrCreate(filepath.Join(t.TempDir(), "k.pem"))
	pin, _ := PinOf(cert)
	c, err := pair(t, cert, pin)
	if err != nil {
		t.Fatal(err)
	}
	c.max = 1000
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.Write(ctx, []byte(strings.Repeat("y", 5000)))
	if _, err := c.Read(ctx); !errors.Is(err, ErrTooLong) {
		t.Fatalf("err = %v, want ErrTooLong", err)
	}
}

func TestReadEndsWithItsContext(t *testing.T) {
	cert, _ := LoadOrCreate(filepath.Join(t.TempDir(), "k.pem"))
	pin, _ := PinOf(cert)
	c, err := pair(t, cert, pin)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.Read(ctx); err == nil {
		t.Fatal("a read with nothing to read returned no error")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("read took %v after its deadline", d)
	}
}
