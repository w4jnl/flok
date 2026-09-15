// Package termtheme asks the terminal for its background colour (OSC 11) and classifies it as
// dark or light. That is the one reliable light/dark signal: the colour-scheme report some
// terminals send (CSI ? 997 n, which tmux's client_theme relies on) follows the OS appearance in
// some of them rather than the actual background, and a tmux in between only answers a pane's
// OSC 11 when it has learned the colours itself. `flok up` asks before it hands the terminal to
// tmux, which also works over ssh.
package termtheme

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// Result is a detected background.
type Result struct {
	Theme string // "dark" or "light"
	BG    string // "#rrggbb"
}

// ErrNoReply means the terminal answered the device-attributes sentinel but not OSC 11: it
// does not report its background.
var ErrNoReply = errors.New("the terminal does not report its background colour")

var (
	osc11Re = regexp.MustCompile(`\x1b\]11;rgba?:([0-9a-fA-F]{1,4})/([0-9a-fA-F]{1,4})/([0-9a-fA-F]{1,4})`)
	da1Re   = regexp.MustCompile(`\x1b\[\?[0-9;]*c`)
)

// Detect queries the controlling terminal. The OSC 11 query is followed by a primary device
// attributes request (DA1), which every terminal answers, so an unsupported query costs one round
// trip instead of the whole timeout; reading until the DA1 answer also leaves nothing in the
// input for whatever reads the terminal next (tmux).
func Detect(timeout time.Duration) (Result, error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()
	fd := int(f.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return Result{}, err
	}
	defer term.Restore(fd, old)
	if _, err := f.WriteString("\x1b]11;?\x1b\\\x1b[c"); err != nil {
		return Result{}, err
	}
	var buf []byte
	chunk := make([]byte, 256)
	deadline := time.Now().Add(timeout)
	for !da1Re.Match(buf) {
		left := time.Until(deadline)
		if left <= 0 {
			break
		}
		var rd unix.FdSet
		rd.Set(fd)
		tv := unix.NsecToTimeval(left.Nanoseconds())
		n, err := unix.Select(fd+1, &rd, nil, nil, &tv)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil || n == 0 {
			break
		}
		k, err := f.Read(chunk)
		if err != nil {
			break
		}
		buf = append(buf, chunk[:k]...)
	}
	if r, ok := Parse(buf); ok {
		return r, nil
	}
	if da1Re.Match(buf) {
		return Result{}, ErrNoReply
	}
	return Result{}, fmt.Errorf("no answer from the terminal within %s", timeout)
}

// Parse reads an OSC 11 reply ("\x1b]11;rgb:RRRR/GGGG/BBBB", 1-4 hex digits per component)
// and classifies it by perceived brightness (ITU-R BT.601): below the middle is dark.
func Parse(reply []byte) (Result, bool) {
	m := osc11Re.FindSubmatch(reply)
	if m == nil {
		return Result{}, false
	}
	var c [3]float64
	for i := range c {
		v, _ := strconv.ParseUint(string(m[i+1]), 16, 32)
		c[i] = float64(v) / float64(uint64(1)<<(4*len(m[i+1]))-1)
	}
	theme := "dark"
	if 0.299*c[0]+0.587*c[1]+0.114*c[2] >= 0.5 {
		theme = "light"
	}
	return Result{Theme: theme, BG: fmt.Sprintf("#%02x%02x%02x", int(c[0]*255+0.5), int(c[1]*255+0.5), int(c[2]*255+0.5))}, true
}
