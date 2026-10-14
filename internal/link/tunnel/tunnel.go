// Package tunnel is the encrypted link between a flok instance and its relay: TLS 1.3 inside
// the WebSocket, so a proxy that inspects the outer HTTPS (an office proxy with its own root
// certificate) passes binary messages it cannot read. The relay holds its own key, a
// self-signed certificate kept with its data; flok trusts exactly that key, named by its pin in
// [link] pin, and no certificate authority, so a proxy that answers with a key of its own is
// refused instead of read. Inside, frames are lines of JSON (encoding/json never writes a raw
// newline), deflated after the auth exchange when both ends speak flok-link/2 (Compress).
package tunnel

import (
	"bufio"
	"bytes"
	"compress/flate"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// ALPN names the protocol inside the tunnel; a later incompatible framing gets a new one.
// flok-link/2 is flok-link/1 deflated: after the auth exchange each direction is one deflate
// stream (Compress).
const (
	ALPN        = "flok-link/1"
	ALPNDeflate = "flok-link/2"
)

// offer is what both ends offer, newest first. The relay takes the first of its own that the
// instance offers too, so two current ends meet at flok-link/2 and an older one at flok-link/1.
var offer = []string{ALPNDeflate, ALPN}

// HandshakeTimeout bounds the TLS handshake inside the WebSocket.
var HandshakeTimeout = 15 * time.Second

// ErrPinMismatch: the key that answered is not the one [link] pin names.
var ErrPinMismatch = errors.New("the relay's key does not match [link] pin")

// ErrTooLong: a frame longer than the connection's limit.
var ErrTooLong = errors.New("frame too long")

// Pin names a certificate's key: "sha256:" and the unpadded base64url SHA-256 of its
// SubjectPublicKeyInfo. It stays the same when the certificate is reissued for the same key.
func Pin(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return "sha256:" + base64.RawURLEncoding.EncodeToString(sum[:])
}

// PinOf is the pin of a loaded key pair's certificate.
func PinOf(c tls.Certificate) (string, error) {
	if len(c.Certificate) == 0 {
		return "", errors.New("no certificate")
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return "", err
	}
	return Pin(leaf), nil
}

// ValidPin reports whether s has a pin's shape.
func ValidPin(s string) bool {
	rest, ok := strings.CutPrefix(s, "sha256:")
	if !ok {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(rest)
	return err == nil && len(b) == sha256.Size
}

// LoadOrCreate reads the relay's key and certificate from path (PEM: the key, then the
// certificate), creating them the first time: a P-256 key and a self-signed certificate that
// does not expire in practice, since trust comes from the pin, not from dates.
func LoadOrCreate(path string) (tls.Certificate, error) {
	if b, err := os.ReadFile(path); err == nil {
		return tls.X509KeyPair(b, b)
	} else if !errors.Is(err, os.ErrNotExist) {
		return tls.Certificate{}, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "flok-relay"},
		DNSNames:     []string{"flok-relay"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(100, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	kder, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	var buf bytes.Buffer
	_ = pem.Encode(&buf, &pem.Block{Type: "PRIVATE KEY", Bytes: kder})
	_ = pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return tls.Certificate{}, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(buf.Bytes(), buf.Bytes())
}

// Conn is one tunnel: frames as lines of JSON over TLS over WebSocket binary messages.
type Conn struct {
	tc      *tls.Conn
	rd      *bufio.Reader
	fw      *flate.Writer // the deflate stream into tc once Compress ran; nil writes straight to tc
	deflate bool          // flok-link/2 agreed
	max     int
	wmu     sync.Mutex
}

// Client opens the tunnel on a dialled WebSocket and checks the relay's key against pin. ctx
// bounds the tunnel's whole life (it ends the WebSocket's reads and writes), max one frame.
func Client(ctx context.Context, ws *websocket.Conn, pin string, max int) (*Conn, error) {
	return client(ctx, ws, pin, max, offer)
}

func client(ctx context.Context, ws *websocket.Conn, pin string, max int, protos []string) (*Conn, error) {
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
		NextProtos: protos,
		ServerName: "flok-relay",
		// No CA, no name, no dates: the pin is the whole of the trust.
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return ErrPinMismatch
			}
			if got := Pin(cs.PeerCertificates[0]); got != pin {
				return fmt.Errorf("%w (it is %s)", ErrPinMismatch, got)
			}
			return nil
		},
	}
	return handshake(ctx, tls.Client(websocket.NetConn(ctx, ws, websocket.MessageBinary), cfg), max)
}

// Server answers the tunnel on an accepted WebSocket with the relay's key.
func Server(ctx context.Context, ws *websocket.Conn, cert tls.Certificate, max int) (*Conn, error) {
	return server(ctx, ws, cert, max, offer)
}

func server(ctx context.Context, ws *websocket.Conn, cert tls.Certificate, max int, protos []string) (*Conn, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, NextProtos: protos, Certificates: []tls.Certificate{cert}}
	return handshake(ctx, tls.Server(websocket.NetConn(ctx, ws, websocket.MessageBinary), cfg), max)
}

func handshake(ctx context.Context, tc *tls.Conn, max int) (*Conn, error) {
	hctx, cancel := context.WithTimeout(ctx, HandshakeTimeout)
	defer cancel()
	if err := tc.HandshakeContext(hctx); err != nil {
		return nil, err
	}
	p := tc.ConnectionState().NegotiatedProtocol
	if p != ALPN && p != ALPNDeflate {
		return nil, fmt.Errorf("the other end speaks %q inside the tunnel, not %s", p, ALPN)
	}
	return &Conn{tc: tc, rd: bufio.NewReaderSize(tc, 64<<10), deflate: p == ALPNDeflate, max: max}, nil
}

// Compress starts flok-link/2's deflate streams, one each way. Both ends call it right after
// the auth exchange, before any other frame, so the token never shares a deflate window with
// what follows: deflated sizes depend on content, and a proxy that can put text into the stream
// and watch the sizes could otherwise guess a secret in the same window. A no-op on
// flok-link/1, and not safe alongside Read or Write.
func (c *Conn) Compress() {
	if !c.deflate || c.fw != nil {
		return
	}
	c.fw, _ = flate.NewWriter(c.tc, flate.BestSpeed) // the level never fails
	// what the reader already holds past the auth frame is the start of the other end's stream
	c.rd = bufio.NewReaderSize(flate.NewReader(c.rd), 64<<10)
}

// Compressed reports that both ends speak flok-link/2: Compress deflates the tunnel.
func (c *Conn) Compressed() bool { return c.deflate }

// Read returns the next frame. ctx's deadline and cancellation end the read, and with it the
// tunnel (a TLS read that timed out cannot be resumed).
func (c *Conn) Read(ctx context.Context) ([]byte, error) {
	defer deadline(ctx, c.tc.SetReadDeadline)()
	var line []byte
	for {
		part, err := c.rd.ReadSlice('\n')
		line = append(line, part...)
		if len(line) > c.max+1 {
			return nil, ErrTooLong
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return line[:len(line)-1], nil
	}
}

// Write sends one frame; safe from several goroutines.
func (c *Conn) Write(ctx context.Context, frame []byte) error {
	if bytes.IndexByte(frame, '\n') >= 0 {
		return errors.New("a frame cannot hold a newline")
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	defer deadline(ctx, c.tc.SetWriteDeadline)()
	line := append(frame[:len(frame):len(frame)], '\n')
	if c.fw == nil {
		_, err := c.tc.Write(line)
		return err
	}
	if _, err := c.fw.Write(line); err != nil {
		return err
	}
	return c.fw.Flush() // a sync flush: the other end reads the frame now, not when a block fills
}

// deadline applies ctx to one read or write: its deadline, and a cancellation that cuts it off.
func deadline(ctx context.Context, set func(time.Time) error) func() {
	dl, _ := ctx.Deadline()
	_ = set(dl)
	stop := context.AfterFunc(ctx, func() { _ = set(time.Now()) })
	return func() { stop() }
}
