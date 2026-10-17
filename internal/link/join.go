package link

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/w4jnl/flok/internal/link/wire"
)

// Joining a relay: `flok link join` has no key yet, so it talks to the relay's API over HTTPS
// the way the phone pairs: it asks for a code (JoinStart), shows it, waits (JoinWait) until a
// member of the tenant approves it, and accepts (JoinAccept) for its key, or withdraws
// (JoinDecline). The relay's answers are small JSON objects; the errors here carry what it said.

// Joiner is the relay as a joining flok sees it.
type Joiner struct {
	Origin string       // the relay as the app addresses it (OriginURL)
	HTTP   *http.Client // nil = a client with ProxyFromEnvironment
}

// JoinStart is the relay's answer to a join request.
type JoinStart struct {
	ID        string    `json:"id"`
	Secret    string    `json:"secret"`
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
	PollS     int       `json:"poll_s"`
}

// JoinState is where a join stands.
type JoinState struct {
	Status     string          `json:"status"` // pending | approved | declined | expired
	Tenant     wire.TenantInfo `json:"tenant"`
	ApprovedBy string          `json:"approved_by"`
	ExpiresAt  time.Time       `json:"expires_at"`
	Error      string          `json:"error"`
}

// Joined is what a flok gets when it accepts: its key, and the relay's pin for the encrypted link.
type Joined struct {
	Token      string          `json:"token"`
	Pin        string          `json:"pin"`
	Tenant     wire.TenantInfo `json:"tenant"`
	Instance   struct{ ID, Name string }
	ApprovedBy string `json:"approved_by"`
	Relay      struct {
		Version string `json:"version"`
	} `json:"relay"`
}

// ErrJoinGone is JoinWait's answer when the join is no longer there: expired, declined or spent.
var ErrJoinGone = errors.New("the join is gone")

func (j Joiner) client() *http.Client {
	if j.HTTP != nil {
		return j.HTTP
	}
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSHandshakeTimeout: 15 * time.Second}}
}

// call is one request to the relay's API; out takes the JSON answer. A status outside 2xx is
// an error with the relay's words; 404 for an unknown route is the relay predating joins.
func (j Joiner) call(ctx context.Context, method, path, bearer string, body, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(j.Origin, "/")+path, rd)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := j.client().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error  string `json:"error"`
			Status string `json:"status"`
		}
		_ = json.Unmarshal(data, &e)
		switch {
		case resp.StatusCode == http.StatusNotFound && e.Error == "" && !strings.HasPrefix(strings.TrimSpace(resp.Header.Get("Content-Type")), "application/json"):
			return resp.StatusCode, errors.New("this relay predates joining by code: update flok-relay")
		case e.Error != "":
			return resp.StatusCode, errors.New(e.Error)
		}
		return resp.StatusCode, fmt.Errorf("the relay answered %s", resp.Status)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, errors.New("the relay's answer was not JSON")
		}
	}
	return resp.StatusCode, nil
}

// Start asks the relay for a code to show.
func (j Joiner) Start(ctx context.Context, name, hostname, version string) (JoinStart, error) {
	var out JoinStart
	_, err := j.call(ctx, http.MethodPost, "/api/enroll", "", map[string]string{"name": name, "hostname": hostname, "version": version}, &out)
	if err == nil && (out.ID == "" || out.Secret == "" || out.Code == "") {
		err = errors.New("the relay's answer lacked the code")
	}
	return out, err
}

// Wait polls once: the relay holds the request until the join moves or its poll time passes.
// ErrJoinGone when the join is no longer there (expired, declined, or spent).
func (j Joiner) Wait(ctx context.Context, s JoinStart) (JoinState, error) {
	var out JoinState
	status, err := j.call(ctx, http.MethodGet, "/api/enroll/"+s.ID, s.Secret, nil, &out)
	switch {
	case err == nil:
		return out, nil
	case status == http.StatusNotFound || status == http.StatusGone:
		return out, fmt.Errorf("%w: %v", ErrJoinGone, err)
	}
	return out, err
}

// Accept takes the approval: the relay keeps a record of this flok and hands over its key.
func (j Joiner) Accept(ctx context.Context, s JoinStart) (Joined, error) {
	var raw struct {
		Joined
		Instance struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"instance"`
	}
	_, err := j.call(ctx, http.MethodPost, "/api/enroll/"+s.ID+"/accept", s.Secret, nil, &raw)
	out := raw.Joined
	out.Instance.ID, out.Instance.Name = raw.Instance.ID, raw.Instance.Name
	if err == nil && out.Token == "" {
		err = errors.New("the relay's answer lacked the key")
	}
	return out, err
}

// Decline withdraws the join (Esc at the approval, or on the way out).
func (j Joiner) Decline(ctx context.Context, s JoinStart) error {
	_, err := j.call(ctx, http.MethodDelete, "/api/enroll/"+s.ID, s.Secret, nil, nil)
	return err
}
