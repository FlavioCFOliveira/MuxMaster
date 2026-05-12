// Package main demonstrates Server-Sent Events (SSE) over MuxMaster with
// PoolRequestBundle enabled — a pattern that is safe to pool because the
// handler does NOT return until the stream completes (or the client
// disconnects). The request object is alive throughout the entire stream,
// so the recycled bundle is only returned to the pool *after* the SSE
// session ends.
//
// Compare with the WebSocket example (examples/websocket/): WebSocket
// uses Hijack(), which keeps the connection alive beyond ServeHTTP's
// lifetime. Pool is unsafe there.
//
// Run:
//
//	go run .
//
// Open in two terminals:
//
//	curl -N http://localhost:8080/events/news      # stream
//	curl -X POST http://localhost:8080/publish \
//	     -d '{"topic":"news","msg":"hello"}'        # publish
//
// Or open http://localhost:8080/ in a browser.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
)

func main() {
	hub := newHub()
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	mux := mm.New()

	// Pool-safe: SSE handlers stay in their for-loop until the client
	// disconnects, so the request is alive for the whole stream.
	mux.PoolRequestBundle = true

	// Pre middleware: applies to every route, including the streaming one.
	mux.Pre(mw.RequestID(), mw.RecovererWithLogger(log))

	// SSE stream: 1-param route, will be 0-alloc dispatch with Pool ON.
	// The handler then opens a stream that keeps r alive — exactly the
	// shape that is safe for pooling.
	mux.GET("/events/:topic", hub.stream)

	// Publish to a topic — POST body is drained inline, so r can be
	// recycled the instant the handler returns.
	mux.POST("/publish", hub.publish)

	// Tiny in-browser demo at the root.
	mux.GET("/", indexHTML)

	// Periodic broadcast: emits a server-side "tick" event every 5 s on the
	// "system" topic so users can see live data even without publishing.
	go hub.tick()

	srv := &http.Server{Addr: ":8080", Handler: mux}
	go func() {
		log.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
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

// ─── Hub: topic → set of subscribers (chan string) ───────────────────────────

type hub struct {
	mu     sync.RWMutex
	topics map[string]map[chan string]struct{}
	done   chan struct{}
}

func newHub() *hub {
	return &hub{
		topics: make(map[string]map[chan string]struct{}),
		done:   make(chan struct{}),
	}
}

func (h *hub) subscribe(topic string) chan string {
	ch := make(chan string, 16)
	h.mu.Lock()
	if h.topics[topic] == nil {
		h.topics[topic] = make(map[chan string]struct{})
	}
	h.topics[topic][ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *hub) unsubscribe(topic string, ch chan string) {
	h.mu.Lock()
	if subs, ok := h.topics[topic]; ok {
		delete(subs, ch)
		if len(subs) == 0 {
			delete(h.topics, topic)
		}
	}
	h.mu.Unlock()
	close(ch)
}

func (h *hub) broadcast(topic, msg string) int {
	h.mu.RLock()
	subs := h.topics[topic]
	delivered := 0
	for ch := range subs {
		select {
		case ch <- msg:
			delivered++
		default: // slow subscriber — drop the message rather than block
		}
	}
	h.mu.RUnlock()
	return delivered
}

func (h *hub) tick() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-h.done:
			return
		case now := <-t.C:
			h.broadcast("system", fmt.Sprintf("tick at %s", now.Format(time.Kitchen)))
		}
	}
}

func (h *hub) close() { close(h.done) }

// ─── Handlers ────────────────────────────────────────────────────────────────

// stream is the SSE endpoint. It writes events until r.Context() is
// cancelled (client disconnect or server shutdown). Pool is safe here:
// the request stays alive for the entire stream.
func (h *hub) stream(w http.ResponseWriter, r *http.Request) {
	topic := mm.PathParam(r, "topic")
	if topic == "" {
		http.Error(w, "missing topic", http.StatusBadRequest)
		return
	}

	// SSE response headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // for nginx proxies
	w.WriteHeader(http.StatusOK)

	// Required: flush so headers are sent immediately, not buffered.
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Subscribe to the topic; unsubscribe on disconnect.
	ch := h.subscribe(topic)
	defer h.unsubscribe(topic, ch)

	// Initial event so the client knows the stream is live.
	fmt.Fprintf(w, "event: ready\ndata: subscribed to %q\n\n", topic)
	flusher.Flush()

	// Streaming loop. r.Context() is the request context — it is cancelled
	// when the client disconnects. SAFE to use under PoolRequestBundle
	// because the handler has not returned yet.
	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			// SSE wire format: event + data lines, terminated by blank line.
			_, _ = fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}

// publish is a regular Handle endpoint. Body is drained inline; nothing
// outlives the handler, so the pooled bundle is recycled cleanly.
func (h *hub) publish(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	var p struct {
		Topic string `json:"topic"`
		Msg   string `json:"msg"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if p.Topic == "" || p.Msg == "" {
		http.Error(w, "topic and msg required", http.StatusUnprocessableEntity)
		return
	}
	n := h.broadcast(p.Topic, p.Msg)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"delivered": n,
		"topic":     p.Topic,
	})
}

// indexHTML serves a tiny demo page that subscribes to /events/system and
// shows each tick.
func indexHTML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, `<!doctype html>
<html>
<head><meta charset="utf-8"><title>MuxMaster SSE</title></head>
<body style="font-family:system-ui;max-width:680px;margin:2em auto">
<h1>MuxMaster · Server-Sent Events</h1>
<p>Subscribed to <code>/events/system</code>. The server pushes a tick every 5 s.</p>
<p>Publish to your own topic: <code>curl -X POST http://localhost:8080/publish -d '{"topic":"news","msg":"hi"}'</code></p>
<ul id="log"></ul>
<script>
const es = new EventSource("/events/system");
es.addEventListener("ready", e => {
  const li = document.createElement("li");
  li.style.color = "#888";
  li.textContent = "[ready] " + e.data;
  log.prepend(li);
});
es.onmessage = e => {
  const li = document.createElement("li");
  li.textContent = new Date().toLocaleTimeString() + " — " + e.data;
  log.prepend(li);
};
</script>
</body></html>`)
}
