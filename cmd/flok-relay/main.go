// flok-relay is the service between flok instances and the phone: instances dial in over a
// WebSocket, the app sees them all, answers prompts and gets pushes. It runs behind your
// reverse proxy (TLS there) as one static binary; see deploy/relay.
package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/coder/websocket"

	"github.com/w4jnl/flok/internal/link"
	"github.com/w4jnl/flok/internal/link/tunnel"
	"github.com/w4jnl/flok/internal/link/wire"
	"github.com/w4jnl/flok/internal/relay"
	"github.com/w4jnl/flok/internal/relay/apns"
)

// version is stamped at build time (-ldflags "-X main.version=v0.6.0").
var version = "dev"

const usage = `flok-relay — the relay between flok instances and the phone

usage: flok-relay <command>

  serve      run the relay (-config relay.toml, -listen :8080, -data /data; env FLOK_RELAY_*)
  tail       connect as the app and print what it would see (-url, -token, -subscribe inst:%12)
  token      print a fresh random token for the config
  tenants    list the tenants with their counts (-data /data; reads the relay's files)
  instances  list the floks that joined by code (-data /data; reads instances.json)
  phones     list the phones paired with the relay (-data /data; reads phones.json)
  pin        print the pin of the relay's key for [link] pin (-data /data; created when missing)
  health     GET /healthz and exit 0 when the relay answers (-url)
  version    print the version

Floks join and tenants are managed from flok itself (flok link join, flok tenant …); the files
here are read as they are, so the listings work with the relay running or not.
`

type fileConfig struct {
	Listen         string   `toml:"listen"`
	DataDir        string   `toml:"data_dir"`
	InstanceTokens []string `toml:"instance_tokens"`
	AdminTokens    []string `toml:"admin_tokens"`
	DeviceTokens   []string `toml:"device_tokens"`
	TunnelOnly     bool     `toml:"tunnel_only"` // refuse links without [link] pin
	APNS           struct {
		KeyFile string `toml:"key_file"`
		KeyID   string `toml:"key_id"`
		TeamID  string `toml:"team_id"`
		Topic   string `toml:"topic"`
		Sandbox bool   `toml:"sandbox"` // no longer used: each device says which service it needs
	} `toml:"apns"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var rc int
	switch os.Args[1] {
	case "serve":
		rc = runServe(os.Args[2:])
	case "tail":
		rc = runTail(os.Args[2:])
	case "token":
		fmt.Println(relay.NewToken())
	case "tenants":
		rc = runTenants(os.Args[2:])
	case "instances":
		rc = runInstances(os.Args[2:])
	case "phones":
		rc = runPhones(os.Args[2:])
	case "pin":
		rc = runPin(os.Args[2:])
	case "health":
		rc = runHealth(os.Args[2:])
	case "version", "--version", "-V":
		fmt.Println("flok-relay", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "flok-relay: unknown command %q\n\n%s", os.Args[1], usage)
		rc = 2
	}
	os.Exit(rc)
}

func env(name, def string) string {
	if v, ok := os.LookupEnv("FLOK_RELAY_" + name); ok {
		return v
	}
	return def
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func runServe(args []string) int {
	fs := flag.NewFlagSet("flok-relay serve", flag.ContinueOnError)
	cfgPath := fs.String("config", env("CONFIG", ""), "TOML config file (optional; the environment overrides it)")
	listen := fs.String("listen", "", "address to listen on (default :8080)")
	data := fs.String("data", "", "data dir for devices.json, phones.json, instances.json, tenants.json and the link key (default from config, else none)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var fc fileConfig
	if *cfgPath != "" {
		if _, err := toml.DecodeFile(*cfgPath, &fc); err != nil {
			fmt.Fprintln(os.Stderr, "flok-relay:", err)
			return 1
		}
	}
	if *listen == "" {
		*listen = env("LISTEN", fc.Listen)
	}
	if *listen == "" {
		*listen = ":8080"
	}
	if *data == "" {
		*data = env("DATA_DIR", fc.DataDir)
	}
	if v := env("INSTANCE_TOKENS", ""); v != "" {
		fc.InstanceTokens = splitList(v)
	}
	if v := env("ADMIN_TOKENS", ""); v != "" {
		fc.AdminTokens = splitList(v)
	}
	if v := env("DEVICE_TOKENS", ""); v != "" {
		fc.DeviceTokens = splitList(v)
	}
	if v := env("TUNNEL_ONLY", ""); v != "" {
		fc.TunnelOnly = v == "1" || strings.EqualFold(v, "true")
	}
	logger := log.New(os.Stdout, "", log.LstdFlags)
	pushCfg := apns.Config{KeyID: env("APNS_KEY_ID", fc.APNS.KeyID), TeamID: env("APNS_TEAM_ID", fc.APNS.TeamID), Topic: env("APNS_TOPIC", fc.APNS.Topic),
		URL: env("APNS_URL", ""), SandboxURL: env("APNS_SANDBOX_URL", "")}
	if v := env("APNS_SANDBOX", ""); v != "" || fc.APNS.Sandbox {
		logger.Printf("push: FLOK_RELAY_APNS_SANDBOX / [apns] sandbox is not needed any more and is ignored: each phone says which service it registered with")
	}
	if b64 := env("APNS_KEY_B64", ""); b64 != "" { // the .p8 on one line, for a container's environment
		b, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(b64), ""))
		if err != nil {
			fmt.Fprintln(os.Stderr, "flok-relay: FLOK_RELAY_APNS_KEY_B64 is not base64:", err)
			return 1
		}
		pushCfg.Key = b
	} else if pemKey := env("APNS_KEY", ""); pemKey != "" {
		pushCfg.Key = []byte(pemKey)
	} else if f := env("APNS_KEY_FILE", fc.APNS.KeyFile); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, "flok-relay: apns key:", err)
			return 1
		}
		pushCfg.Key = b
	}
	var pusher relay.Pusher
	if p, err := apns.New(pushCfg); err != nil {
		fmt.Fprintln(os.Stderr, "flok-relay:", err)
		return 1
	} else if p != nil {
		pusher = p
		logger.Printf("push: APNs for %s (sandbox or production, as each phone registered)", pushCfg.Topic)
	} else {
		logger.Printf("push: off (no APNs key)")
	}
	var linkKey *tls.Certificate
	if *data == "" {
		logger.Printf("link: no encrypted link (no data dir to keep the relay's key in)")
	} else if k, err := tunnel.LoadOrCreate(filepath.Join(*data, linkKeyFile)); err != nil {
		logger.Printf("link: no encrypted link: %v", err)
	} else {
		linkKey = &k
		pin, _ := tunnel.PinOf(k)
		logger.Printf("link pin %s (for [link] pin; links without it are %s)", pin, map[bool]string{true: "refused", false: "taken too"}[fc.TunnelOnly])
	}
	if fc.TunnelOnly && linkKey == nil {
		fmt.Fprintln(os.Stderr, "flok-relay: tunnel_only needs the relay's key, which needs a data dir")
		return 1
	}
	srv, err := relay.New(relay.Config{InstanceTokens: fc.InstanceTokens, AdminTokens: fc.AdminTokens, DeviceTokens: fc.DeviceTokens, DataDir: *data, Push: pusher,
		Logf: logger.Printf, Version: version, LinkKey: linkKey, TunnelOnly: fc.TunnelOnly})
	if err != nil {
		fmt.Fprintln(os.Stderr, "flok-relay:", err)
		return 1
	}
	hs := &http.Server{Addr: *listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
	}()
	if *data == "" {
		logger.Printf("phones: kept in memory (no data dir): pairings and joins do not survive a restart")
	}
	logger.Printf("flok-relay %s listening on %s (%d instance tokens, %d admin tokens, %d device tokens; %d tenants, %d floks joined, %d phones paired)",
		version, *listen, len(fc.InstanceTokens), len(fc.AdminTokens), len(fc.DeviceTokens), srv.TenantCount(), srv.InstanceCount(), srv.PhoneCount())
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, "flok-relay:", err)
		return 1
	}
	srv.Close()
	return 0
}

// linkKeyFile holds the relay's key and self-signed certificate for the encrypted link.
const linkKeyFile = "link-key.pem"

// runPhones lists the phones paired with the relay, from its phones.json: for an admin at the
// container. Pairing and forgetting go through an instance (flok phone pair | forget).
func runPhones(args []string) int {
	fs := flag.NewFlagSet("flok-relay phones", flag.ContinueOnError)
	data := fs.String("data", env("DATA_DIR", "/data"), "the relay's data dir")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	list, err := relay.ListPhones(*data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "flok-relay:", err)
		return 1
	}
	if len(list) == 0 {
		fmt.Println("no phones paired (flok phone pair on an instance shows the code)")
		return 0
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tTENANT\tPAIRED\tLAST SEEN\tVIA")
	for _, p := range list {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", p.ID, p.Name, p.Tenant, p.Paired.Local().Format("2006-01-02 15:04"), seenAt(p.LastSeen), p.Instance)
	}
	_ = tw.Flush()
	return 0
}

func seenAt(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Local().Format("2006-01-02 15:04")
}

// runTenants lists the tenants from the relay's files: the default (the config's tokens) and
// those made by flok tenant approve, with their counts.
func runTenants(args []string) int {
	fs := flag.NewFlagSet("flok-relay tenants", flag.ContinueOnError)
	data := fs.String("data", env("DATA_DIR", "/data"), "the relay's data dir")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	list, err := relay.ListTenants(*data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "flok-relay:", err)
		return 1
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tFLOKS\tPHONES\tCREATED\tID")
	for _, t := range list {
		created := "the relay's own"
		if !t.Created.IsZero() {
			created = t.Created.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%s\n", t.Name, t.Instances, t.Phones, created, t.ID)
	}
	_ = tw.Flush()
	return 0
}

// runInstances lists the floks that joined by code, from instances.json.
func runInstances(args []string) int {
	fs := flag.NewFlagSet("flok-relay instances", flag.ContinueOnError)
	data := fs.String("data", env("DATA_DIR", "/data"), "the relay's data dir")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	list, err := relay.ListInstances(*data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "flok-relay:", err)
		return 1
	}
	if len(list) == 0 {
		fmt.Println("no flok joined by code (flok link join on a flok shows the code; floks with a token from the config are not listed here)")
		return 0
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTENANT\tHOST\tJOINED\tLAST SEEN\tAPPROVED BY\tID")
	for _, r := range list {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.Name, r.Tenant, r.Hostname, r.Joined.Local().Format("2006-01-02 15:04"), seenAt(r.LastSeen), r.ApprovedBy, r.ID)
	}
	_ = tw.Flush()
	return 0
}

// runPin prints the pin of the relay's key, creating the key the way serve would.
func runPin(args []string) int {
	fs := flag.NewFlagSet("flok-relay pin", flag.ContinueOnError)
	data := fs.String("data", env("DATA_DIR", "/data"), "the relay's data dir")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	k, err := tunnel.LoadOrCreate(filepath.Join(*data, linkKeyFile))
	if err != nil {
		fmt.Fprintln(os.Stderr, "flok-relay:", err)
		return 1
	}
	pin, err := tunnel.PinOf(k)
	if err != nil {
		fmt.Fprintln(os.Stderr, "flok-relay:", err)
		return 1
	}
	fmt.Println(pin)
	return 0
}

func runHealth(args []string) int {
	fs := flag.NewFlagSet("flok-relay health", flag.ContinueOnError)
	u := fs.String("url", "http://127.0.0.1:8080/healthz", "the /healthz URL")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	hc := &http.Client{Timeout: 4 * time.Second}
	resp, err := hc.Get(*u)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	fmt.Print(string(body))
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// runTail is the app's view from a terminal: the instances frame, then every frame as it comes,
// one JSON line each; -subscribe asks for a pane's screen; -answer types one answer and exits.
func runTail(args []string) int {
	fs := flag.NewFlagSet("flok-relay tail", flag.ContinueOnError)
	u := fs.String("url", env("URL", ""), "the relay (https://flok.example.net or wss://…/api/ws)")
	tok := fs.String("token", env("DEVICE_TOKEN", ""), "a device token")
	sub := fs.String("subscribe", "", "instance:pane whose screen to stream (e2e: beta:%3)")
	ans := fs.String("answer", "", "instance:pane to answer, with -text and -keys, then exit")
	text := fs.String("text", "", "text for -answer")
	keysArg := fs.String("keys", "", "keys for -answer, comma-separated (Enter,Escape,…)")
	count := fs.Int("n", 0, "exit after this many frames (0 = until the connection ends)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ws := link.NormalizeURL(*u)
	if ws == "" || *tok == "" {
		fmt.Fprintln(os.Stderr, "flok-relay tail: -url and -token are needed")
		return 2
	}
	ws = strings.TrimSuffix(ws, "/link") + "/api/ws"
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	hdr := http.Header{"Authorization": {"Bearer " + *tok}}
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	conn, resp, err := websocket.Dial(dctx, ws, &websocket.DialOptions{HTTPHeader: hdr, CompressionMode: websocket.CompressionContextTakeover})
	cancel()
	if err != nil {
		if resp != nil {
			fmt.Fprintln(os.Stderr, "flok-relay tail:", resp.Status)
		} else {
			fmt.Fprintln(os.Stderr, "flok-relay tail:", err)
		}
		return 1
	}
	defer conn.CloseNow()
	conn.SetReadLimit(wire.MaxFrame)
	send := func(f wire.Frame) error {
		data, _ := json.Marshal(f)
		return conn.Write(ctx, websocket.MessageText, data)
	}
	split := func(s string) (string, string) { // instance:paneref; the pane ref may hold a colon itself (beta:%3)
		i := strings.Index(s, ":")
		if i < 0 {
			return "", s
		}
		return s[:i], s[i+1:]
	}
	if *sub != "" {
		inst, pane := split(*sub)
		if err := send(wire.Frame{Type: wire.TypeSubscribe, Instance: inst, Pane: pane, On: true}); err != nil {
			fmt.Fprintln(os.Stderr, "flok-relay tail:", err)
			return 1
		}
	}
	if *ans != "" {
		inst, pane := split(*ans)
		aa := answerArg(pane, *text, *keysArg)
		a := wire.Frame{Type: wire.TypeAnswer, ID: "tail", Instance: inst, Answer: &aa}
		if err := send(a); err != nil {
			fmt.Fprintln(os.Stderr, "flok-relay tail:", err)
			return 1
		}
	}
	enc := json.NewEncoder(os.Stdout)
	seen := 0
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return 0
			}
			fmt.Fprintln(os.Stderr, "flok-relay tail:", err)
			return 1
		}
		var f wire.Frame
		if json.Unmarshal(data, &f) != nil {
			continue
		}
		_ = enc.Encode(f)
		seen++
		if *ans != "" && f.Type == wire.TypeAck && f.ID == "tail" {
			if f.Error != "" {
				return 1
			}
			return 0
		}
		if *count > 0 && seen >= *count {
			return 0
		}
	}
}
