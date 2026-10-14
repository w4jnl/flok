package tunnel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// pair runs a tunnel server on an httptest WebSocket and dials it with pin, both ends offering
// what the package offers; the server side echoes every frame back upper-cased, the first one
// (the auth exchange) as it is and the rest after Compress, as the relay does.
func pair(t *testing.T, cert tls.Certificate, pin string) (*Conn, error) {
	c, _, err := pairWith(t, cert, pin, offer, offer)
	return c, err
}

// pairWith is pair with the protocols each end offers (an older end offers flok-link/1 alone),
// and a count of the bytes the server reads off its socket.
func pairWith(t *testing.T, cert tls.Certificate, pin string, srvProtos, cliProtos []string) (*Conn, *atomic.Int64, error) {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		c, err := server(r.Context(), ws, cert, 1<<20, srvProtos)
		if err != nil {
			return
		}
		for first := true; ; first = false {
			f, err := c.Read(r.Context())
			if err != nil {
				return
			}
			if err := c.Write(r.Context(), bytes.ToUpper(f)); err != nil {
				return
			}
			if first {
				c.Compress()
			}
		}
	}))
	read := &atomic.Int64{}
	srv.Listener = countingListener{srv.Listener, read}
	srv.Start()
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.CloseNow() })
	c, err := client(ctx, ws, pin, 1<<20, cliProtos)
	return c, read, err
}

type countingListener struct {
	net.Listener
	read *atomic.Int64
}

func (l countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return countingConn{c, l.read}, nil
}

type countingConn struct {
	net.Conn
	read *atomic.Int64
}

func (c countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.read.Add(int64(n))
	return n, err
}

// echo writes f and checks the server's upper-cased answer.
func echo(t *testing.T, c *Conn, f string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, []byte(f)); err != nil {
		t.Fatal(err)
	}
	got, err := c.Read(ctx)
	if err != nil || string(got) != strings.ToUpper(f) {
		t.Fatalf("echo of %d bytes: %d bytes, %v", len(f), len(got), err)
	}
}

// screen is a pane's worth of text as one frame, lines that do not repeat each other; edit
// changes one of them.
func screen(edit int) string {
	var b strings.Builder
	for i := 0; i < 80; i++ {
		sum := sha256.Sum256([]byte{byte(i)})
		if i == edit {
			sum[0]++
		}
		fmt.Fprintf(&b, "line %02d │ %x\\n", i, sum[:20])
	}
	return b.String()
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
	echo(t, c, `{"type":"auth"}`)
	if !c.Compressed() {
		t.Fatal("two current ends did not agree on " + ALPNDeflate)
	}
	c.Compress()
	big := strings.Repeat("x", 200<<10) // spans many TLS records and reader buffers
	for _, f := range []string{`{"type":"hello"}`, big, screen(-1), screen(3), `{"type":"bye"}`} {
		echo(t, c, f)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, []byte("a\nb")); err == nil {
		t.Error("a frame with a newline was written")
	}
}

// After the auth exchange the tunnel is deflated with one window for the whole connection: a
// frame costs what deflate makes of it, and one that repeats the last but for a line costs
// little more than that line.
func TestCompressDeflatesWithOneWindow(t *testing.T) {
	cert, _ := LoadOrCreate(filepath.Join(t.TempDir(), "k.pem"))
	pin, _ := PinOf(cert)
	c, read, err := pairWith(t, cert, pin, offer, offer)
	if err != nil {
		t.Fatal(err)
	}
	echo(t, c, `{"type":"auth"}`)
	c.Compress()
	cost := func(f string) int64 {
		before := read.Load()
		echo(t, c, f)
		return read.Load() - before
	}
	xs := cost(strings.Repeat("x", 200<<10))
	if xs > 4<<10 {
		t.Errorf("200 KB of x crossed in %d bytes", xs)
	}
	first, again := cost(screen(-1)), cost(screen(40))
	t.Logf("on the wire: 200 KB of x %d bytes, a %d-byte screen %d, again with a line changed %d", xs, len(screen(-1)), first, again)
	if first > int64(len(screen(-1))*3/4) {
		t.Errorf("a %d-byte screen crossed in %d bytes", len(screen(-1)), first)
	}
	if again > 300 {
		t.Errorf("the same screen with one line changed crossed in %d bytes (the first in %d)", again, first)
	}
}

// An instance or relay from before flok-link/2 offers flok-link/1 alone; the other end meets it
// there, Compress does nothing and the frames go as they are.
func TestOlderEndsMeetAtFlokLink1(t *testing.T) {
	cert, _ := LoadOrCreate(filepath.Join(t.TempDir(), "k.pem"))
	pin, _ := PinOf(cert)
	old := []string{ALPN}
	for name, protos := range map[string][2][]string{"older relay": {old, offer}, "older instance": {offer, old}} {
		t.Run(name, func(t *testing.T) {
			c, read, err := pairWith(t, cert, pin, protos[0], protos[1])
			if err != nil {
				t.Fatal(err)
			}
			echo(t, c, `{"type":"auth"}`)
			if c.Compressed() {
				t.Fatal("an end that offers flok-link/1 alone was met at " + ALPNDeflate)
			}
			c.Compress()
			before := read.Load()
			echo(t, c, strings.Repeat("x", 200<<10))
			if n := read.Load() - before; n < 200<<10 {
				t.Errorf("200 KB crossed in %d bytes on flok-link/1", n)
			}
		})
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

// The limit counts what a frame inflates to: a few hundred bytes of deflate that would make
// half a megabyte stop at it too.
func TestReadStopsAtTheLimit(t *testing.T) {
	cert, _ := LoadOrCreate(filepath.Join(t.TempDir(), "k.pem"))
	pin, _ := PinOf(cert)
	for _, deflated := range []bool{false, true} {
		c, err := pair(t, cert, pin)
		if err != nil {
			t.Fatal(err)
		}
		if deflated {
			echo(t, c, `{"type":"auth"}`)
			c.Compress()
		}
		c.max = 1000
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = c.Write(ctx, []byte(strings.Repeat("y", 500<<10)))
		if _, err := c.Read(ctx); !errors.Is(err, ErrTooLong) {
			t.Fatalf("deflated %v: err = %v, want ErrTooLong", deflated, err)
		}
		cancel()
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
