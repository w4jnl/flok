package apns

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testKey(t *testing.T) (*ecdsa.PrivateKey, []byte) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return k, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func TestSendOverHTTP2WithProviderToken(t *testing.T) {
	key, pemKey := testKey(t)
	type got struct {
		proto, path, auth, topic, collapse, pushType string
		body                                         map[string]any
	}
	rec := make(chan got, 4)
	status := http.StatusOK
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(data, &body)
		rec <- got{r.Proto, r.URL.Path, r.Header.Get("authorization"), r.Header.Get("apns-topic"), r.Header.Get("apns-collapse-id"), r.Header.Get("apns-push-type"), body}
		w.WriteHeader(status)
		if status != http.StatusOK {
			io.WriteString(w, `{"reason":"Unregistered"}`)
		}
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	c, err := New(Config{Key: pemKey, KeyID: "KEY1234567", TeamID: "TEAM123456", Topic: "nl.w4j.flok", URL: srv.URL, HTTP: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	badge := 2
	err = c.Send(context.Background(), Device{Token: "abc123"}, Notification{Title: "api", Subtitle: "home · Hugo", Body: "● needs you: perm:Bash", Category: "FLOK_PERMISSION",
		ThreadID: "home", CollapseID: "home:%1", Badge: &badge, Sound: "flok-request.caf", TimeSensitive: true, Mutable: true, Data: map[string]any{"pane": "%1", "aps": "ignored"}})
	if err != nil {
		t.Fatal(err)
	}
	g := <-rec
	if g.proto != "HTTP/2.0" || g.path != "/3/device/abc123" || g.topic != "nl.w4j.flok" || g.collapse != "home:%1" || g.pushType != "alert" {
		t.Fatalf("request: %+v", g)
	}
	aps := g.body["aps"].(map[string]any)
	alert := aps["alert"].(map[string]any)
	if alert["title"] != "api" || alert["subtitle"] != "home · Hugo" || aps["category"] != "FLOK_PERMISSION" || aps["badge"].(float64) != 2 ||
		aps["interruption-level"] != "time-sensitive" || aps["sound"] != "flok-request.caf" || aps["mutable-content"].(float64) != 1 || g.body["pane"] != "%1" {
		t.Fatalf("payload: %+v", g.body)
	}
	// the provider token: ES256 over header.claims, verifiable with the key's public half
	tok := strings.TrimPrefix(g.auth, "bearer ")
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt: %q", tok)
	}
	hdr, _ := base64.RawURLEncoding.DecodeString(parts[0])
	claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
	if !strings.Contains(string(hdr), `"kid":"KEY1234567"`) || !strings.Contains(string(claims), `"iss":"TEAM123456"`) {
		t.Fatalf("jwt header %s claims %s", hdr, claims)
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(sig) != 64 || !ecdsa.Verify(&key.PublicKey, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("signature does not verify")
	}
	// the token is reused, not re-signed per push
	_ = c.Send(context.Background(), Device{Token: "abc123"}, Notification{Title: "x", Body: "y"})
	if g2 := <-rec; g2.auth != g.auth {
		t.Fatal("token was re-signed within its lifetime")
	}
	c.tokenAt = time.Now().Add(-time.Hour)
	_ = c.Send(context.Background(), Device{Token: "abc123"}, Notification{Title: "x", Body: "y"})
	if g3 := <-rec; g3.auth == g.auth {
		t.Fatal("an hour-old token must be refreshed")
	}
	status = http.StatusGone
	if err := c.Send(context.Background(), Device{Token: "abc123"}, Notification{Title: "x", Body: "y"}); err != ErrUnregistered {
		t.Fatalf("410: %v", err)
	}
	<-rec
}

// One key, two services: each device goes to the one its token came from.
func TestSendPicksTheDevicesService(t *testing.T) {
	_, pemKey := testKey(t)
	paths := make(chan string, 4)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { paths <- r.URL.Path }))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	c, err := New(Config{Key: pemKey, KeyID: "KEY1234567", TeamID: "TEAM123456", Topic: "nl.w4j.flok",
		URL: srv.URL + "/production", SandboxURL: srv.URL + "/sandbox", HTTP: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []Device{{Token: "fromxcode", Sandbox: true}, {Token: "fromtestflight"}} {
		if err := c.Send(context.Background(), d, Notification{Title: "x", Body: "y"}); err != nil {
			t.Fatal(err)
		}
	}
	if a, b := <-paths, <-paths; a != "/sandbox/3/device/fromxcode" || b != "/production/3/device/fromtestflight" {
		t.Fatalf("paths: %s, %s", a, b)
	}
	// without overrides: Apple's two hosts
	c, _ = New(Config{Key: pemKey, KeyID: "KEY1234567", TeamID: "TEAM123456", Topic: "nl.w4j.flok"})
	if c.base != ProductionHost || c.sandbox != SandboxHost {
		t.Fatalf("hosts: %s %s", c.base, c.sandbox)
	}
	// one override (the e2e stand-in) serves both
	c, _ = New(Config{Key: pemKey, KeyID: "KEY1234567", TeamID: "TEAM123456", Topic: "nl.w4j.flok", URL: "http://127.0.0.1:1"})
	if c.base != "http://127.0.0.1:1" || c.sandbox != "http://127.0.0.1:1" {
		t.Fatalf("hosts with URL: %s %s", c.base, c.sandbox)
	}
}

func TestNewRefuses(t *testing.T) {
	if c, err := New(Config{}); c != nil || err != nil {
		t.Fatal("no key: off, no error")
	}
	_, pemKey := testKey(t)
	if _, err := New(Config{Key: pemKey}); err == nil {
		t.Fatal("ids are needed")
	}
	if _, err := New(Config{Key: []byte("junk"), KeyID: "k", TeamID: "t", Topic: "x"}); err == nil {
		t.Fatal("junk key accepted")
	}
}
