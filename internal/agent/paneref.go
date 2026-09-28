package agent

import (
	"errors"
	"regexp"
	"strings"
)

// LocalHost is the display name of the local inner tmux server. Its Host value is "", so the
// string form of a local pane is the bare tmux id that every existing key, file and JSON field
// already uses; only remote panes carry a "host:" prefix.
const LocalHost = "local"

var (
	paneIDRe = regexp.MustCompile(`^%\d+$`)
	// HostNameRe is what a remote host may be called: short, safe in file names, ssh command
	// lines and tmux window names, never mistakable for an option. Case is kept as typed (an ssh
	// alias like dockerAMS), but names are unique regardless of case (hosts.Set.Add), because
	// the per-host dirs live on a case-insensitive filesystem on macOS.
	HostNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)
)

// PaneRef names a pane on a host. Host "" is the local server.
type PaneRef struct {
	Host string
	ID   string // tmux pane id, "%12"
}

// String is "%12" for a local pane and "beta:%12" for a remote one.
func (r PaneRef) String() string {
	if r.Host == "" {
		return r.ID
	}
	return r.Host + ":" + r.ID
}

// ParsePaneRef accepts "%12", "local:%12" and "beta:%12". The host keeps the spelling given;
// callers that compare it with registered hosts resolve it first (hosts.Set.Get ignores case).
func ParsePaneRef(s string) (PaneRef, error) {
	host, id := "", s
	if i := strings.LastIndex(s, ":"); i >= 0 {
		host, id = s[:i], s[i+1:]
		if !HostNameRe.MatchString(host) {
			return PaneRef{}, errors.New("host name must match " + HostNameRe.String() + ", got " + strconvQuote(host))
		}
		if strings.EqualFold(host, LocalHost) {
			host = ""
		}
	}
	if !paneIDRe.MatchString(id) {
		return PaneRef{}, errors.New("pane id must look like %12, got " + strconvQuote(id))
	}
	return PaneRef{Host: host, ID: id}, nil
}

func strconvQuote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }
