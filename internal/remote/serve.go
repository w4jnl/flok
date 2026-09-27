package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/w4jnl/flok/internal/merge"
	"github.com/w4jnl/flok/internal/nav"
	"github.com/w4jnl/flok/internal/poller"
	"github.com/w4jnl/flok/internal/remote/proto"
	"github.com/w4jnl/flok/internal/state"
	"github.com/w4jnl/flok/internal/tmux"
)

// ServeDeps is what `flok serve --stdio` hands Serve.
type ServeDeps struct {
	Hello proto.Hello
	// NewPoller builds the host's pipeline with the given sound callback (which Serve turns
	// into event frames); Serve closes the poller when it returns.
	NewPoller func(sound func(pane, kind string)) *poller.Poller
	Inner     tmux.Client  // for goto
	Store     *state.Store // visible → terminal-focus marker
	In        io.Reader
	Out       io.Writer
	Heartbeat time.Duration // resend the last snapshot after this much silence; 0 = 5 s
	Debugf    func(string, ...any)
}

// Serve is the remote end of a full-mode connection: hello, then the merged view whenever
// it changes (or every Heartbeat), sound events, and answers to goto/seen/visible/ping.
// It returns when In ends (the local side went away) or ctx is cancelled.
func Serve(ctx context.Context, d ServeDeps) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	debugf := func(string, ...any) {}
	if d.Debugf != nil {
		debugf = d.Debugf
	}
	var wmu sync.Mutex
	write := func(f proto.Frame) error {
		wmu.Lock()
		defer wmu.Unlock()
		return proto.Write(d.Out, f)
	}
	if err := write(proto.Frame{Type: proto.TypeHello, Hello: &d.Hello}); err != nil {
		return err
	}
	p := d.NewPoller(func(pane, kind string) {
		_ = write(proto.Frame{Type: proto.TypeEvent, Event: &proto.Event{Pane: pane, Kind: kind}})
	})
	defer p.Close()

	snaps := make(chan merge.Snapshot, 1)
	onChange := func(s merge.Snapshot) { // keep the latest, never block the poller
		for {
			select {
			case snaps <- s:
				return
			default:
				select {
				case <-snaps:
				default:
				}
			}
		}
	}
	cmds := make(chan func(), 16)
	pollErr := make(chan error, 1)
	go func() { pollErr <- poller.RunLoop(ctx, p, onChange, cmds) }()

	frames := make(chan proto.Frame, 16)
	inDone := make(chan error, 1)
	go func() {
		r := proto.NewReader(d.In)
		r.Noise = func(l string) { debugf("noise: %s", l) }
		for {
			f, err := r.Next()
			if err != nil {
				inDone <- err
				return
			}
			select {
			case frames <- f:
			case <-ctx.Done():
				return
			}
		}
	}()
	enqueue := func(f func()) {
		select {
		case cmds <- f:
		default:
			debugf("command dropped: poller busy")
		}
	}

	hb := d.Heartbeat
	if hb <= 0 {
		hb = 5 * time.Second
	}
	tick := time.NewTicker(hb)
	defer tick.Stop()
	var lastBody []byte
	var last proto.Snapshot
	var lastSent time.Time
	sendSnap := func(s proto.Snapshot) error {
		last = s
		lastSent = time.Now()
		return write(proto.Frame{Type: proto.TypeSnap, Snap: &s})
	}
	for {
		select {
		case s := <-snaps:
			ps := proto.FromMerge(s)
			cmp := ps
			cmp.TakenAt = time.Time{}
			body, _ := json.Marshal(cmp)
			if bytes.Equal(body, lastBody) {
				last = ps
				continue
			}
			lastBody = body
			if err := sendSnap(ps); err != nil {
				return err
			}
		case <-tick.C:
			if lastBody != nil && time.Since(lastSent) >= hb {
				if err := sendSnap(last); err != nil {
					return err
				}
			}
		case f := <-frames:
			switch f.Type {
			case proto.TypePing:
				if err := write(proto.Frame{Type: proto.TypePong}); err != nil {
					return err
				}
			case proto.TypeSeen:
				pane := f.Pane
				if pane != "" {
					enqueue(func() { p.MarkSeen(pane) })
				}
			case proto.TypeVisible:
				if d.Store != nil {
					_ = d.Store.SetTerminalFocus(f.On)
				}
			case proto.TypeGoto:
				if f.Goto == nil {
					continue
				}
				g := *f.Goto
				enqueue(func() {
					tty := p.Snap().Focus.ClientTTY
					if err := nav.Go(d.Inner, tty, g.Session, g.Window, g.Pane); err != nil {
						_ = write(proto.Frame{Type: proto.TypeError, Error: "goto: " + err.Error()})
					} else if g.Pane != "" {
						p.MarkSeen(g.Pane)
					}
				})
			}
		case err := <-inDone:
			if err == io.EOF {
				return nil
			}
			return err
		case err := <-pollErr:
			if ctx.Err() != nil {
				return nil
			}
			return err
		case <-ctx.Done():
			return nil
		}
	}
}
