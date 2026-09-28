// Package hosts is the registry of remote tmux servers flok shows next to the local one:
// $FLOK_STATE/hosts.json, edited by `flok host …` and the sidebar, read by the launcher and the
// remote manager. Names are the cross-host key (agent.PaneRef); the local server has none.
package hosts

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/w4jnl/flok/internal/agent"
	"github.com/w4jnl/flok/internal/state"
)

const (
	// File is the registry's name in the state dir; File+".lock" serializes Update.
	File = "hosts.json"
	// Version is bumped when a change would confuse an older reader.
	Version = 1
)

// Mode says how flok reaches a host's agents.
type Mode string

const (
	// ModeFull runs `flok serve --stdio` on the host: hooks, the Claude registry and screen
	// rules evaluated there, the merged view streamed back.
	ModeFull Mode = "full"
	// ModePlain drives the host's tmux over ssh from here: titles and screen rules only, for
	// hosts where flok cannot be installed.
	ModePlain Mode = "plain"
)

// Host is one remote tmux server.
type Host struct {
	Name          string    `json:"name"`
	Target        string    `json:"target"` // what `ssh` accepts: alias, host, user@host
	Mode          Mode      `json:"mode"`
	Socket        string    `json:"socket,omitempty"`  // remote `tmux -L` socket; "" = tmux's default
	Enabled       bool      `json:"enabled"`           // connect at flok up; disconnect persists false
	Flok          string    `json:"flok,omitempty"`    // absolute path of the remote flok; "" = [hosts] serve_command
	Session       string    `json:"session,omitempty"` // session the work pane attaches to or creates; "" = the most recent
	Term          string    `json:"term,omitempty"`    // TERM for the attach when the host lacks tmux-256color
	AddedAt       time.Time `json:"added_at"`
	LastConnected time.Time `json:"last_connected,omitzero"`
}

// Set is the whole registry, in the order hosts were added (the servers panel keeps it).
type Set struct {
	Version int    `json:"version"`
	Hosts   []Host `json:"hosts"`
}

var (
	targetRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@:-]*$`)
	socketRe  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	flokRe    = regexp.MustCompile(`^/[A-Za-z0-9._@+/-]+$`)
	sessionRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_@-]*$`)
	termRe    = socketRe
)

// ValidateName checks a host name: short, safe in file names, ssh command lines and tmux window
// names, and not the local server's label. The name is flok's label for the host; ssh gets the
// target, so an ssh alias works as a name in its own spelling.
func ValidateName(name string) error {
	if strings.EqualFold(name, agent.LocalHost) {
		return fmt.Errorf("%q names the local server", name)
	}
	if !agent.HostNameRe.MatchString(name) {
		msg := fmt.Sprintf("host name %q: use 1-32 letters, digits, _ and -, starting with a letter or digit", name)
		if alt := SuggestName(name); alt != "" {
			msg += fmt.Sprintf(" (e.g. %s)", alt)
		}
		return errors.New(msg)
	}
	return nil
}

// SuggestName turns s into a valid host name: every run of other characters becomes "-",
// leading and trailing separators go, and the result is cut to 32. "" when nothing valid is left.
func SuggestName(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range s {
		if r < utf8.RuneSelf && (r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	name := strings.TrimLeft(b.String(), "-_")
	if len(name) > 32 {
		name = name[:32]
	}
	name = strings.TrimRight(name, "-")
	if name == "" || !agent.HostNameRe.MatchString(name) || strings.EqualFold(name, agent.LocalHost) {
		return ""
	}
	return name
}

// NameFromTarget derives a host name from an ssh target for `flok host add <target>`: the user
// and a port go, a DNS name gives its first label (jaro@beta.example.org → beta), an IPv4
// address its dashed form, and an alias stays as typed (dockerAMS). IPv6 needs an explicit name.
func NameFromTarget(target string) (string, error) {
	host := target
	if i := strings.LastIndex(host, "@"); i >= 0 {
		host = host[i+1:]
	}
	if strings.Count(host, ":") == 1 {
		host = host[:strings.Index(host, ":")]
	}
	ip := net.ParseIP(host)
	if (ip != nil && ip.To4() == nil) || strings.Contains(host, ":") { // IPv6
		return "", fmt.Errorf("cannot name a host after %q; give it a name: flok host add <name> %s", target, target)
	}
	if ip == nil {
		host, _, _ = strings.Cut(host, ".")
	}
	name := SuggestName(host)
	if name == "" {
		return "", fmt.Errorf("cannot name a host after %q; give it a name: flok host add <name> %s", target, target)
	}
	return name, nil
}

// Validate checks every field that ends up in a command line. Fixed charsets and no leading
// "-" keep the ssh and tmux argv unambiguous; the values are still shell-quoted when sent.
func Validate(h Host) error {
	if err := ValidateName(h.Name); err != nil {
		return err
	}
	if !targetRe.MatchString(h.Target) {
		return fmt.Errorf("target %q: use what ssh accepts (alias, host, user@host); letters, digits and . _ @ : -", h.Target)
	}
	switch h.Mode {
	case ModeFull, ModePlain:
	default:
		return fmt.Errorf("mode %q: full (flok serve on the host) or plain (tmux only)", h.Mode)
	}
	if h.Socket != "" && !socketRe.MatchString(h.Socket) {
		return fmt.Errorf("socket %q: letters, digits and . _ -", h.Socket)
	}
	if h.Flok != "" && !flokRe.MatchString(h.Flok) {
		return fmt.Errorf("flok %q: an absolute path", h.Flok)
	}
	if h.Session != "" && !sessionRe.MatchString(h.Session) {
		return fmt.Errorf("session %q: letters, digits and _ @ -", h.Session)
	}
	if h.Term != "" && !termRe.MatchString(h.Term) {
		return fmt.Errorf("term %q: a terminfo name", h.Term)
	}
	return nil
}

// Get returns the host called name, in any case; the result carries the registered spelling.
func (s Set) Get(name string) (Host, bool) {
	for _, h := range s.Hosts {
		if strings.EqualFold(h.Name, name) {
			return h, true
		}
	}
	return Host{}, false
}

// Enabled returns the hosts to connect, in registry order.
func (s Set) Enabled() []Host {
	var out []Host
	for _, h := range s.Hosts {
		if h.Enabled {
			out = append(out, h)
		}
	}
	return out
}

// Names lists the host names in registry order.
func (s Set) Names() []string {
	out := make([]string, 0, len(s.Hosts))
	for _, h := range s.Hosts {
		out = append(out, h.Name)
	}
	return out
}

// Add appends a validated host; a name in use, in any case, is an error (the per-host dirs
// live on a case-insensitive filesystem on macOS).
func (s *Set) Add(h Host) error {
	if err := Validate(h); err != nil {
		return err
	}
	if old, ok := s.Get(h.Name); ok {
		return fmt.Errorf("host %q exists; flok host set %s … changes it", old.Name, old.Name)
	}
	s.Hosts = append(s.Hosts, h)
	return nil
}

// Set replaces the host called name (in any case) with fn's edit of it, validated; a missing
// host is an error. The name itself does not change.
func (s *Set) Set(name string, fn func(*Host)) error {
	for i := range s.Hosts {
		if !strings.EqualFold(s.Hosts[i].Name, name) {
			continue
		}
		h := s.Hosts[i]
		fn(&h)
		h.Name = s.Hosts[i].Name
		if err := Validate(h); err != nil {
			return err
		}
		s.Hosts[i] = h
		return nil
	}
	return fmt.Errorf("no host %q (flok host list)", name)
}

// AttachChanged says whether the work pane's ssh must be rebuilt for the change from a to b.
func AttachChanged(a, b Host) bool {
	return a.Target != b.Target || a.Socket != b.Socket || a.Session != b.Session || a.Term != b.Term
}

// Remove drops the host called name (in any case) and says whether it was there.
func (s *Set) Remove(name string) bool {
	for i, h := range s.Hosts {
		if strings.EqualFold(h.Name, name) {
			s.Hosts = append(s.Hosts[:i], s.Hosts[i+1:]...)
			return true
		}
	}
	return false
}

// SetEnabled flips a host's enabled flag (name in any case) and says whether the host exists.
func (s *Set) SetEnabled(name string, on bool) bool {
	for i := range s.Hosts {
		if strings.EqualFold(s.Hosts[i].Name, name) {
			s.Hosts[i].Enabled = on
			return true
		}
	}
	return false
}

// Path is the registry file under stateDir.
func Path(stateDir string) string { return filepath.Join(stateDir, File) }

// Dir is where a host's local files live (a plain-mode store, cached probes); `flok host remove`
// prunes it.
func Dir(stateDir, name string) string { return filepath.Join(stateDir, "hosts", name) }

// Load reads the registry; a missing file is an empty registry, not an error.
func Load(stateDir string) (Set, error) {
	s := Set{Version: Version}
	data, err := os.ReadFile(Path(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("%s: %w", Path(stateDir), err)
	}
	if s.Version > Version {
		return s, fmt.Errorf("%s: version %d is newer than this flok understands (%d)", Path(stateDir), s.Version, Version)
	}
	s.Version = Version
	return s, nil
}

// Save writes the registry atomically. Callers that read-modify-write use Update instead.
func Save(stateDir string, s Set) error {
	s.Version = Version
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	return state.WriteJSONAtomic(Path(stateDir), s)
}

// Update loads the registry, applies fn and saves it, all under the registry lock, so the CLI,
// the sidebar and the remote manager (last_connected) never lose each other's writes. fn
// returning an error leaves the file untouched.
func Update(stateDir string, fn func(*Set) error) (Set, error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return Set{}, err
	}
	lock, err := os.OpenFile(Path(stateDir)+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return Set{}, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return Set{}, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	s, err := Load(stateDir)
	if err != nil {
		return s, err
	}
	if err := fn(&s); err != nil {
		return s, err
	}
	return s, Save(stateDir, s)
}
