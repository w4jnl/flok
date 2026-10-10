// Package apns sends alerts through the Apple Push Notification service over HTTP/2 with a
// provider token (ES256 JWT from the .p8 key of your developer account). No dependencies: the
// standard library speaks HTTP/2 and signs P-256.
package apns

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Config of the provider.
type Config struct {
	Key     []byte // the .p8 file (PKCS#8 PEM) from the developer account
	KeyID   string // its key id (10 characters)
	TeamID  string // the team id
	Topic   string // the app's bundle id
	Sandbox bool   // development builds (Xcode) register with the sandbox service
	URL     string // overrides the service URL (tests); "" = Apple's, by Sandbox
	HTTP    *http.Client
}

// Hosts of the service.
const (
	Production = "https://api.push.apple.com"
	SandboxURL = "https://api.sandbox.push.apple.com"
)

// Notification is one alert.
type Notification struct {
	Title         string
	Body          string
	Category      string // the app's notification category (actions)
	ThreadID      string // groups alerts in Notification Center
	CollapseID    string // a later alert with the same id replaces the earlier one
	Badge         *int
	Sound         string // "default", or "" for none
	TimeSensitive bool   // breaks through Focus modes the user allowed it for
	Data          map[string]any
}

// ErrUnregistered is returned for a device token the service no longer knows (the app was
// removed): drop the token.
var ErrUnregistered = errors.New("device token is not registered any more")

// Error is a refusal by the service.
type Error struct {
	Status int
	Reason string
}

func (e *Error) Error() string { return fmt.Sprintf("apns: %d %s", e.Status, e.Reason) }

// Client sends to one app.
type Client struct {
	cfg  Config
	key  *ecdsa.PrivateKey
	base string
	http *http.Client

	mu       sync.Mutex
	token    string
	tokenAt  time.Time
	tokenTTL time.Duration
}

// New parses the key; nil, nil when cfg has no key (push off).
func New(cfg Config) (*Client, error) {
	if len(cfg.Key) == 0 {
		return nil, nil
	}
	if cfg.KeyID == "" || cfg.TeamID == "" || cfg.Topic == "" {
		return nil, errors.New("apns: key_id, team_id and topic are all needed")
	}
	block, _ := pem.Decode(cfg.Key)
	if block == nil {
		return nil, errors.New("apns: the key is not PEM (expected the .p8 file)")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("apns: key: %w", err)
	}
	ec, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("apns: the key is not an EC (P-256) key")
	}
	base := cfg.URL
	if base == "" {
		base = Production
		if cfg.Sandbox {
			base = SandboxURL
		}
	}
	hc := cfg.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{ForceAttemptHTTP2: true, Proxy: http.ProxyFromEnvironment,
			TLSHandshakeTimeout: 15 * time.Second, IdleConnTimeout: 10 * time.Minute}}
	}
	return &Client{cfg: cfg, key: ec, base: base, http: hc, tokenTTL: 50 * time.Minute}, nil
}

// Topic is the app's bundle id.
func (c *Client) Topic() string { return c.cfg.Topic }

// Send delivers one alert to a device token.
func (c *Client) Send(ctx context.Context, device string, n Notification) error {
	tok, err := c.jwt(time.Now())
	if err != nil {
		return err
	}
	aps := map[string]any{"alert": map[string]string{"title": n.Title, "body": n.Body}}
	if n.Sound != "" {
		aps["sound"] = n.Sound
	}
	if n.Badge != nil {
		aps["badge"] = *n.Badge
	}
	if n.Category != "" {
		aps["category"] = n.Category
	}
	if n.ThreadID != "" {
		aps["thread-id"] = n.ThreadID
	}
	if n.TimeSensitive {
		aps["interruption-level"] = "time-sensitive"
	}
	payload := map[string]any{"aps": aps}
	for k, v := range n.Data {
		if k != "aps" {
			payload[k] = v
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/3/device/"+device, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("authorization", "bearer "+tok)
	req.Header.Set("apns-topic", c.cfg.Topic)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", "10")
	req.Header.Set("apns-expiration", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
	if n.CollapseID != "" {
		req.Header.Set("apns-collapse-id", trunc(n.CollapseID, 64))
	}
	req.Header.Set("content-type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	var r struct {
		Reason string `json:"reason"`
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	_ = json.Unmarshal(data, &r)
	if resp.StatusCode == http.StatusGone || r.Reason == "Unregistered" || r.Reason == "BadDeviceToken" {
		return ErrUnregistered
	}
	if r.Reason == "" {
		r.Reason = string(bytes.TrimSpace(data))
	}
	return &Error{Status: resp.StatusCode, Reason: r.Reason}
}

// jwt is the provider token, reused for 50 minutes (Apple wants one younger than an hour and
// no more than one refresh per 20 minutes).
func (c *Client) jwt(now time.Time) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && now.Sub(c.tokenAt) < c.tokenTTL {
		return c.token, nil
	}
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	signing := enc(map[string]string{"alg": "ES256", "kid": c.cfg.KeyID}) + "." + enc(map[string]any{"iss": c.cfg.TeamID, "iat": now.Unix()})
	sum := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, c.key, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64) // r || s, each left-padded to 32 bytes (JWS, not ASN.1)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	c.token, c.tokenAt = signing+"."+base64.RawURLEncoding.EncodeToString(sig), now
	return c.token, nil
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
