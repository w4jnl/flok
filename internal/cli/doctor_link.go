package cli

import (
	"context"
	"net/http"
	"time"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/link"
	"github.com/w4jnl/flok/internal/link/tunnel"
	"github.com/w4jnl/flok/internal/snapshot"
	"github.com/w4jnl/flok/internal/state"
)

// doctorLink reports the relay link: the running sidebar's own state when it publishes one,
// else a GET of the relay's /healthz (through HTTPS_PROXY like the link itself).
func doctorLink(cfg config.Config, add func(level, format string, args ...any)) {
	settings := state.New(config.StateDir()).LinkSettings(cfg.Link.URL, cfg.Link.Token, cfg.Link.Pin, cfg.Link.Admin)
	u := settings.URL
	if u == "" {
		add("ok", "link: off ([link] url in config.toml, or flok link join <relay>, connects this flok to your relay, for the phone app)")
		return
	}
	ws := link.NormalizeURL(u)
	if ws == "" {
		add("fail", "link: [link] url %q is not one flok can dial (wss://host/link, or https://host)", u)
		return
	}
	name := cfg.InstanceName()
	if j := settings.Joined; j != nil {
		add("ok", "link: joined %s as %s on %s, approved by %s; its key is in %s", j.Tenant, j.Name, j.Joined.Local().Format("2006-01-02"), j.ApprovedBy, state.LinkJoinedFile)
		if cfg.Link.URL != "" && link.OriginURL(cfg.Link.URL) != j.Relay {
			add("warn", "link: [link] url %q in config.toml is not used: the join record names %s (flok link leave goes back to the config)", cfg.Link.URL, j.Relay)
		} else if cfg.Link.Token != "" {
			add("ok", "link: [link] token in config.toml is not used while this flok is joined by code")
		}
	}
	admin := "[link] admin"
	if settings.AdminKept {
		admin = "the admin token in " + state.LinkAdminFile + " (flok link admin)"
		if cfg.Link.Admin != "" && cfg.Link.Admin != settings.Admin {
			add("warn", "link: [link] admin in config.toml is not used: %s wins (flok link admin --clear goes back to the config)", admin)
		}
	}
	switch {
	case settings.Token == "" && settings.Admin == "":
		add("warn", "link: [link] token is empty; the relay will refuse %s (flok link join <relay> gets it a key of its own)", name)
	case settings.Token == "":
		add("ok", "link: %s alone links %s into the relay's own tenant, with the tenants in its hands (flok tenant; flok link join moves it to a tenant of its own)", admin, name)
	case settings.Admin != "":
		add("ok", "link: %s is set: %s may manage the relay's tenants (flok tenant)", admin, name)
	}
	how := ""
	if p := settings.Pin; p != "" {
		if !tunnel.ValidPin(p) {
			add("fail", "link: [link] pin %q is not a pin (sha256:… as `flok-relay pin` prints it)", p)
			return
		}
		ws, how = link.TunnelURL(u), ", encrypted to the pinned key"
	}
	if s, f := snapshot.Load(config.StateDir(), time.Now()); f == snapshot.Fresh && s.Link != nil && s.SidebarAlive() {
		if link.State(s.Link.State) == link.Off {
			add("ok", "link: switched off since %s (flok link on connects %q to %s again)", s.Link.Since.Local().Format("15:04:05"), name, ws)
			return
		}
		level := "ok"
		if s.Link.State != "connected" {
			level = "warn"
		}
		detail := ""
		if s.Link.Detail != "" {
			detail = ": " + s.Link.Detail
		}
		if s.Link.Compressed {
			how += ", compressed"
		}
		add(level, "link: %s to %s as %q%s%s (since %s)", s.Link.State, ws, name, how, detail, s.Link.Since.Local().Format("15:04:05"))
		return
	}
	if readLink(config.StateDir()).off {
		add("ok", "link: switched off (flok link on); it stays off when flok starts")
	}
	health := link.HealthURL(u)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, health, nil)
	hc := &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}}
	resp, err := hc.Do(req)
	if err != nil {
		add("warn", "link: relay at %s does not answer: %v", health, err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		add("warn", "link: relay at %s answered %s (not a flok relay?)", health, resp.Status)
		return
	}
	add("ok", "link: relay at %s answers; the sidebar connects as %q%s", health, name, how)
}
