package relay

import (
	"context"
	"encoding/json"
	"time"

	"github.com/coder/websocket"

	"github.com/w4jnl/flok/internal/link/tunnel"
	"github.com/w4jnl/flok/internal/link/wire"
)

// frameConn is one connection's frames: WebSocket text messages on /link and /api/ws, lines of
// JSON inside the TLS tunnel on /link/tls.
type frameConn interface {
	Read(ctx context.Context) ([]byte, error)
	Write(ctx context.Context, frame []byte) error
	Close(code websocket.StatusCode, reason string) error
	CloseNow() error
}

type wsConn struct{ ws *websocket.Conn }

func (c wsConn) Read(ctx context.Context) ([]byte, error) {
	_, data, err := c.ws.Read(ctx)
	return data, err
}

func (c wsConn) Write(ctx context.Context, frame []byte) error {
	return c.ws.Write(ctx, websocket.MessageText, frame)
}

func (c wsConn) Close(code websocket.StatusCode, reason string) error {
	return c.ws.Close(code, reason)
}
func (c wsConn) CloseNow() error { return c.ws.CloseNow() }

type tunnelConn struct {
	ws *websocket.Conn
	t  *tunnel.Conn
}

func (c tunnelConn) Read(ctx context.Context) ([]byte, error) { return c.t.Read(ctx) }

func (c tunnelConn) Write(ctx context.Context, frame []byte) error { return c.t.Write(ctx, frame) }

// Close tells the reason inside the tunnel, as an error frame, and closes the WebSocket with the
// code alone: the outer close frame is what a proxy reads, and a reason can name the instance.
func (c tunnelConn) Close(code websocket.StatusCode, reason string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if data, err := json.Marshal(wire.Frame{Type: wire.TypeError, Error: reason}); err == nil {
		_ = c.t.Write(ctx, data)
	}
	return c.ws.Close(code, "")
}

func (c tunnelConn) CloseNow() error { return c.ws.CloseNow() }
