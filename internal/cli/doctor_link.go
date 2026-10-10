package cli

import (
	"context"
	"net/http"
	"time"

	"github.com/w4jnl/flok/internal/config"
	"github.com/w4jnl/flok/internal/link"
	"github.com/w4jnl/flok/internal/snapshot"
)

// doctorLink reports the phone link: the running sidebar's own state when it publishes one,
// else a GET of the relay's /healthz (through HTTPS_PROXY like the link itself).
func doctorLink(cfg config.Config, add func(level, format string, args ...any)) {
	u := cfg.Link.URL
	if u == "" {
		add("ok", "link: off ([link] url connects this flok to your relay, for the phone app)")
		return
	}
	ws := link.NormalizeURL(u)
	if ws == "" {
		add("fail", "link: [link] url %q is not one flok can dial (wss://host/link, or https://host)", u)
		return
	}
	name := cfg.InstanceName()
	if cfg.Link.Token == "" {
		add("warn", "link: [link] token is empty; the relay will refuse %s", name)
	}
	if s, f := snapshot.Load(config.StateDir(), time.Now()); f == snapshot.Fresh && s.Link != nil && s.SidebarAlive() {
		level := "ok"
		if s.Link.State != "connected" {
			level = "warn"
		}
		detail := ""
		if s.Link.Detail != "" {
			detail = ": " + s.Link.Detail
		}
		add(level, "link: %s to %s as %q%s (since %s)", s.Link.State, ws, name, detail, s.Link.Since.Local().Format("15:04:05"))
		return
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
	add("ok", "link: relay at %s answers; the sidebar connects as %q", health, name)
}
