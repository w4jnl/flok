package push

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type got struct {
	auth, title, prio, tags, ctype, body string
}

func server(t *testing.T) (*httptest.Server, func() []got) {
	var mu sync.Mutex
	var all []got
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		all = append(all, got{r.Header.Get("Authorization"), r.Header.Get("Title"), r.Header.Get("Priority"), r.Header.Get("Tags"), r.Header.Get("Content-Type"), string(b)})
		mu.Unlock()
	}))
	t.Cleanup(s.Close)
	return s, func() []got { mu.Lock(); defer mu.Unlock(); return append([]got(nil), all...) }
}

func wait(t *testing.T, n int, all func() []got) []got {
	for i := 0; i < 100; i++ {
		if g := all(); len(g) >= n {
			return g
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waited for %d requests, got %d", n, len(all()))
	return nil
}

func TestNtfyAndJSON(t *testing.T) {
	s, all := server(t)
	c := New(s.URL+"/flok", "secret", "", nil)
	c.Send(Event{Instance: "home", Agent: "api", Pane: "%1", Kind: "blocked", Reason: "permission:Bash"})
	c.Send(Event{Instance: "home", Host: "beta", Agent: "docs", Pane: "beta:%3", Kind: "done"})
	c.Send(Event{Instance: "home", Agent: "api", Pane: "%1", Kind: "error", Reason: "error"})
	g := wait(t, 3, all)
	title := func(raw string) string {
		s, err := new(mime.WordDecoder).DecodeHeader(raw)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if g[0].auth != "Bearer secret" || title(g[0].title) != "home · api" || g[0].body != "needs you: perm:Bash" || g[0].prio != "high" || g[0].tags != "warning" {
		t.Fatalf("blocked: %+v", g[0])
	}
	if !strings.HasPrefix(g[0].title, "=?utf-8?b?") { // the dot is not ASCII: an encoded word, which ntfy decodes
		t.Fatalf("title header must be ASCII: %q", g[0].title)
	}
	if title(g[1].title) != "home · beta/docs" || g[1].body != "finished" || g[1].prio != "default" {
		t.Fatalf("done: %+v", g[1])
	}
	if g[2].body != "failed" || g[2].tags != "x" {
		t.Fatalf("error: %+v", g[2])
	}
	j := New(s.URL+"/hook", "", "json", nil)
	j.Send(Event{Instance: "office", Agent: "web", Pane: "%7", Kind: "blocked", Reason: "question"})
	g = wait(t, 4, all)
	var ev Event
	if err := json.Unmarshal([]byte(g[3].body), &ev); err != nil || g[3].ctype != "application/json" || ev.Instance != "office" || ev.Kind != "blocked" || ev.Reason != "question" || g[3].auth != "" {
		t.Fatalf("json: %+v %v", g[3], err)
	}
	if New("  ", "", "", nil) != nil {
		t.Fatal("no url: no client")
	}
	var nilClient *Client
	nilClient.Send(Event{}) // a nil client is a no-op
}
