// Package main demonstrates multipart file upload over MuxMaster with
// PoolRequestBundle enabled — including the **critical** body-drain
// pattern that makes spawning a goroutine pool-safe.
//
// Why this matters:
//
//   - Under PoolRequestBundle, the *http.Request is returned to the pool
//     the instant the handler returns. If you spawn a goroutine that
//     reads r.Body or r.MultipartReader() asynchronously, the goroutine
//     observes a recycled request (use-after-free).
//   - The fix: drain everything you need into local values BEFORE spawning.
//
// This example shows the safe and the unsafe patterns side by side.
//
// Run:
//
//	mkdir -p /tmp/muxmaster-uploads
//	go run .
//
// Upload:
//
//	curl -F "file=@/path/to/some/file" http://localhost:8080/upload
//	curl -F "file=@/path/to/some/file" \
//	     -F "file=@/path/to/another/file" \
//	     http://localhost:8080/multi
//	curl -F "file=@/path/to/some/file" http://localhost:8080/async
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

const (
	uploadDir = "/tmp/muxmaster-uploads"
	maxBody   = 32 << 20 // 32 MiB cap to keep the demo bounded
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		log.Error("mkdir failed", "err", err)
		os.Exit(1)
	}

	mux := mm.New()
	// Pool-safe ONLY if every upload handler drains the body before return.
	// The three handlers below are correctly written for this contract.
	mux.PoolRequestBundle = true

	mux.Pre(mw.RequestID(), mw.RecovererWithLogger(log))

	// Each handler illustrates a different correctness pattern.
	mux.POST("/upload", singleUpload)         // 1 file, sync
	mux.POST("/multi", multiUpload)           // N files, sync
	mux.POST("/async", asyncProcessUpload)    // drain → spawn goroutine

	mux.GET("/", indexHTML)

	srv := &http.Server{
		Addr:        ":8080",
		Handler:     mux,
		ReadTimeout: 60 * time.Second, // upload-friendly
	}
	go func() {
		log.Info("listening", "addr", srv.Addr, "uploadDir", uploadDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// ─── Single-file upload (synchronous) ────────────────────────────────────────

// singleUpload uses ParseMultipartForm. The whole upload is materialised
// before the handler returns — bundle recycling happens normally.
func singleUpload(w http.ResponseWriter, r *http.Request) {
	// Cap the body so a hostile uploader can't OOM us.
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		http.Error(w, "parse error: "+err.Error(), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing 'file' field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	sum, written, err := saveFile(file, header.Filename)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"filename": header.Filename,
		"size":     written,
		"sha256":   sum,
	})
}

// ─── Multiple files in one POST ──────────────────────────────────────────────

func multiUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		http.Error(w, "parse error: "+err.Error(), http.StatusBadRequest)
		return
	}

	type result struct {
		Filename string `json:"filename"`
		Size     int64  `json:"size"`
		SHA256   string `json:"sha256"`
	}
	var results []result

	for _, headers := range r.MultipartForm.File {
		for _, h := range headers {
			f, err := h.Open()
			if err != nil {
				http.Error(w, "open: "+err.Error(), http.StatusInternalServerError)
				return
			}
			sum, n, err := saveFile(f, h.Filename)
			_ = f.Close()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			results = append(results, result{Filename: h.Filename, Size: n, SHA256: sum})
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"files": results})
}

// ─── Async processing — the body-drain pattern ───────────────────────────────

// asyncProcessUpload demonstrates the SAFE way to spawn a goroutine from
// a handler when PoolRequestBundle is enabled.
//
// The KEY rule: drain the body into local values (bytes, strings) BEFORE
// spawning. Never capture r in the goroutine.
//
// ❌ DON'T:
//
//	go func() {
//	    io.Copy(dst, r.Body) // r is recycled — Body is now another request's
//	}()
//
// ✅ DO:
//
//	body, _ := io.ReadAll(r.Body)  // drain inline
//	go func() {
//	    process(body)              // operate on captured value
//	}()
func asyncProcessUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		http.Error(w, "parse error: "+err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing 'file' field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Snapshot EVERYTHING the goroutine needs into local values.
	// `data` and `filename` are captured by value; `r` is not captured.
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, file); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := buf.Bytes()
	filename := header.Filename
	requestID := mw.GetRequestID(r.Context())

	// Now the goroutine has no reference to r — safe under Pool.
	go func() {
		// Simulate background processing — e.g. virus scan, thumbnailing.
		time.Sleep(500 * time.Millisecond)
		sum := sha256.Sum256(data)
		fmt.Fprintf(os.Stderr,
			"[bg] processed req=%s file=%q size=%d sha256=%x\n",
			requestID, filename, len(data), sum)
	}()

	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"accepted":   true,
		"filename":   filename,
		"size_bytes": len(data),
		"request_id": requestID,
	})
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func saveFile(src multipart.File, filename string) (string, int64, error) {
	if filename == "" {
		return "", 0, fmt.Errorf("empty filename")
	}
	// Reject path traversal.
	safe := filepath.Base(filename)
	if safe == "." || safe == ".." {
		return "", 0, fmt.Errorf("invalid filename")
	}

	dst, err := os.Create(filepath.Join(uploadDir, safe))
	if err != nil {
		return "", 0, fmt.Errorf("create: %w", err)
	}
	defer dst.Close()

	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(dst, hasher), src)
	if err != nil {
		return "", 0, fmt.Errorf("copy: %w", err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), written, nil
}

func indexHTML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, `<!doctype html>
<html>
<head><meta charset="utf-8"><title>MuxMaster Upload</title></head>
<body style="font-family:system-ui;max-width:680px;margin:2em auto">
<h1>MuxMaster · Multipart Upload</h1>
<form method="POST" action="/upload" enctype="multipart/form-data">
<p><input type="file" name="file"><button type="submit">Upload (sync)</button></p>
</form>
<form method="POST" action="/multi" enctype="multipart/form-data">
<p><input type="file" name="files" multiple><button type="submit">Upload multiple</button></p>
</form>
<form method="POST" action="/async" enctype="multipart/form-data">
<p><input type="file" name="file"><button type="submit">Upload (async processing)</button></p>
</form>
<p>All endpoints save into <code>/tmp/muxmaster-uploads</code> and respond with SHA-256 + size.</p>
</body></html>`)
}
