// Package main demonstrates WebSocket support over MuxMaster — including
// **WHEN NOT TO USE** PoolRequestBundle.
//
// WebSocket upgrade hijacks the underlying TCP connection from the
// http.ResponseWriter. The connection remains open AFTER ServeHTTP
// returns — the WebSocket handler then runs in its own read/write goroutines
// that own the hijacked conn.
//
// This pattern is **incompatible with PoolRequestBundle** at face value:
//
//   - The hijacked connection has no reference to *http.Request, so the
//     request object itself CAN safely be recycled.
//   - BUT if you (or a library you call) keep a reference to r or values
//     derived from r alive after the upgrade, you get a use-after-free.
//
// gorilla/websocket internally calls Hijack() and abandons the request
// pointer before returning — so in practice it IS safe to use with the
// pool, provided your own code does not retain r past the upgrade call.
//
// To keep the example unambiguous and to teach the safer default:
//   - This example uses MuxMaster's default (non-pooled) Handle path.
//   - The trade-off: ~110 ns / 384 B on the upgrade dispatch — but the
//     upgrade itself takes microseconds (handshake, headers), so the
//     routing overhead is irrelevant at this scale.
//   - The post-upgrade read/write loops use Pre + RecovererWithLogger
//     and are independent of the routing layer's performance.
//
// Run:
//
//	go run .
//
// Open multiple browser tabs at http://localhost:8080 to chat. Or:
//
//	# subscribe via wscat
//	wscat -c ws://localhost:8080/ws
//	# publish via the POST endpoint
//	curl -X POST http://localhost:8080/broadcast -d 'hello everyone'
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
	"github.com/gorilla/websocket"
)

// Upgrader configuration.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// Accept any origin for demo purposes. PRODUCTION: validate origin.
	CheckOrigin: func(r *http.Request) bool { return true },
}

func main() {
	hub := newHub()
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	mux := mm.New()
	// PoolRequestBundle is INTENTIONALLY left false. See file header.
	// mux.PoolRequestBundle = false

	mux.Pre(mw.RequestID(), mw.RecovererWithLogger(log))

	mux.GET("/ws", hub.upgradeHandler(log))   // WebSocket endpoint
	mux.POST("/broadcast", hub.broadcastHTTP) // server-initiated broadcast
	mux.GET("/", indexHTML)

	go hub.run()

	srv := &http.Server{Addr: ":8080", Handler: mux}
	go func() {
		log.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	hub.close()
}

// ─── Hub: in-memory broadcast bus over WebSocket clients ─────────────────────

type client struct {
	conn *websocket.Conn
	send chan []byte
}

type hub struct {
	mu      sync.RWMutex
	clients map[*client]struct{}
	in      chan []byte
	join    chan *client
	leave   chan *client
	done    chan struct{}
}

func newHub() *hub {
	return &hub{
		clients: make(map[*client]struct{}),
		in:      make(chan []byte, 256),
		join:    make(chan *client),
		leave:   make(chan *client),
		done:    make(chan struct{}),
	}
}

func (h *hub) run() {
	for {
		select {
		case <-h.done:
			return
		case c := <-h.join:
			h.mu.Lock()
			h.clients[c] = struct{}{}
			h.mu.Unlock()
		case c := <-h.leave:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
			h.mu.Unlock()
		case msg := <-h.in:
			h.mu.RLock()
			for c := range h.clients {
				select {
				case c.send <- msg:
				default: // slow client — drop
				}
			}
			h.mu.RUnlock()
		}
	}
}

func (h *hub) close() { close(h.done) }

// upgradeHandler returns the handler that performs the WebSocket upgrade.
//
// Lifetime notes:
//   - upgrader.Upgrade() calls Hijack() internally. After it returns
//     successfully, the *http.Request is no longer needed by the conn.
//   - The conn's Read/Write goroutines below DO NOT capture r — they
//     only capture the *websocket.Conn returned by Upgrade.
//   - This is the canonical safe pattern.
func (h *hub) upgradeHandler(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Snapshot anything from r we want to keep BEFORE the upgrade,
		// because gorilla/websocket may zero parts of r during Hijack.
		requestID := mw.GetRequestID(r.Context())
		remote := r.RemoteAddr

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Error("upgrade failed", "err", err)
			return
		}
		// From this point on, the handler returns immediately but the
		// conn goroutines live on. Nothing they touch references r.

		c := &client{conn: conn, send: make(chan []byte, 32)}
		h.join <- c

		log.Info("client joined", "req", requestID, "peer", remote)

		// Two goroutines per connection: a write pump and a read pump.
		// On disconnect, both exit and the client is removed from the hub.
		go h.writePump(c)
		go h.readPump(c, log, requestID, remote)
	}
}

func (h *hub) readPump(c *client, log *slog.Logger, requestID, remote string) {
	defer func() {
		h.leave <- c
		_ = c.conn.Close()
		log.Info("client left", "req", requestID, "peer", remote)
	}()
	c.conn.SetReadLimit(1 << 16) // 64 KiB
	_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		// Echo into the hub. Real apps would parse/validate first.
		h.in <- msg
	}
}

func (h *hub) writePump(c *client) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() { ticker.Stop(); _ = c.conn.Close() }()
	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok { // hub closed our channel
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// broadcastHTTP lets external services push into the hub via HTTP POST.
// Standard Handle path — body is drained inline so it would be safe even
// with PoolRequestBundle enabled.
func (h *hub) broadcastHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<14))
	if err != nil || len(body) == 0 {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	h.in <- body
	w.WriteHeader(http.StatusAccepted)
}

func indexHTML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, `<!doctype html>
<html>
<head><meta charset="utf-8"><title>MuxMaster WebSocket</title></head>
<body style="font-family:system-ui;max-width:680px;margin:2em auto">
<h1>MuxMaster · WebSocket chat</h1>
<input id="msg" placeholder="Type a message and press Enter…" style="width:80%">
<button id="send">Send</button>
<ul id="log" style="list-style:none;padding:0"></ul>
<script>
const ws = new WebSocket("ws://" + location.host + "/ws");
ws.onopen = () => log.prepend(li("[open]", "#888"));
ws.onclose = () => log.prepend(li("[closed]", "#a44"));
ws.onmessage = e => log.prepend(li(e.data, "#000"));
function li(text, color) {
  const x = document.createElement("li");
  x.style.color = color;
  x.textContent = text;
  return x;
}
msg.addEventListener("keydown", e => { if (e.key === "Enter") send.click(); });
send.onclick = () => { if (msg.value) { ws.send(msg.value); msg.value = ""; } };
</script>
</body></html>`)
}
