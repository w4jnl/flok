// Package push posts a short notification to an HTTP endpoint when an agent needs the user or
// finishes: an ntfy topic (https://ntfy.sh/<topic> or your own server), or any endpoint that
// takes the JSON form. It is the phone side of flok until the relay and the app exist, and
// the webhook afterwards.
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Event is one transition worth telling the user about.
type Event struct {
	Instance string    `json:"instance"`         // this flok's name ([link] name, default the hostname)
	Host     string    `json:"host,omitempty"`   // the remote host the agent runs on; "" = this machine
	Agent    string    `json:"agent"`            // the row's name
	Pane     string    `json:"pane"`             // pane ref: %12, beta:%12
	Kind     string    `json:"kind"`             // blocked | done | error
	Reason   string    `json:"reason,omitempty"` // permission:Bash, question, elicitation, error
	At       time.Time `json:"at"`
}

// Sender takes events; Client is the HTTP one, tests record them.
type Sender interface{ Send(Event) }

// Client posts events from a bounded queue on its own goroutine, so a slow or dead endpoint
// never holds the sidebar; what does not fit the queue is dropped with a log line.
type Client struct {
	URL, Token, Format string
	HTTP               *http.Client
	Logf               func(string, ...any)

	once  sync.Once
	queue chan Event
}

// New returns a client for url ("" = nil, nothing is sent); format is "ntfy" or "json".
func New(url, token, format string, logf func(string, ...any)) *Client {
	if strings.TrimSpace(url) == "" {
		return nil
	}
	if format == "" {
		format = "ntfy"
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Client{URL: strings.TrimSpace(url), Token: token, Format: format,
		HTTP: &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}}, Logf: logf}
}

// Send queues an event; it never blocks.
func (c *Client) Send(ev Event) {
	if c == nil {
		return
	}
	c.once.Do(func() {
		c.queue = make(chan Event, 64)
		go c.run()
	})
	select {
	case c.queue <- ev:
	default:
		c.Logf("push: queue full, dropped %s %s", ev.Kind, ev.Pane)
	}
}

func (c *Client) run() {
	for ev := range c.queue {
		if err := c.post(ev); err != nil {
			c.Logf("push: %s %s: %v", ev.Kind, ev.Pane, err)
			continue
		}
		c.Logf("push: sent %s %s to %s", ev.Kind, ev.Pane, c.URL)
	}
}

func (c *Client) post(ev Event) error {
	req, err := c.Request(context.Background(), ev)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s: HTTP %d", c.URL, resp.StatusCode)
	}
	return nil
}

// Request builds the HTTP request for an event: ntfy's headers and a plain text body, or the
// JSON event.
func (c *Client) Request(ctx context.Context, ev Event) (*http.Request, error) {
	var req *http.Request
	var err error
	if c.Format == "json" {
		body, _ := json.Marshal(ev)
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
	} else {
		title, text, priority, tags := Message(ev)
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, c.URL, strings.NewReader(text))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "text/plain; charset=utf-8")
		req.Header.Set("Title", headerValue(title))
		req.Header.Set("Priority", priority)
		req.Header.Set("Tags", tags)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return req, nil
}

// headerValue keeps a header ASCII: HTTP headers carry no UTF-8, and ntfy (like mail) reads
// RFC 2047 encoded words, so "home · api" travels as =?utf-8?b?...?=.
func headerValue(s string) string {
	for _, r := range s {
		if r > 127 {
			return mime.BEncoding.Encode("utf-8", s)
		}
	}
	return s
}

// Message is the human form of an event: a title ("home · api", "home · beta/api"), a line
// ("needs you: perm:Bash", "finished", "failed"), an ntfy priority and tag.
func Message(ev Event) (title, text, priority, tags string) {
	who := ev.Agent
	if ev.Host != "" {
		who = ev.Host + "/" + who
	}
	title = ev.Instance + " · " + who
	switch ev.Kind {
	case "blocked":
		r := strings.Replace(ev.Reason, "permission:", "perm:", 1)
		if r == "" {
			r = "input"
		}
		return title, "needs you: " + r, "high", "warning"
	case "error":
		return title, "failed", "high", "x"
	default:
		return title, "finished", "default", "white_check_mark"
	}
}
