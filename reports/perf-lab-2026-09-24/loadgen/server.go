package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"time"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
	"github.com/FlavioCFOliveira/MuxMaster/middleware"
)

var jwtSecret = []byte("perf-lab-2026-09-24-loadgen-secret-material")

func hs256Token(sub string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(
		`{"sub":"` + sub + `","exp":` + strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10) + `}`))
	signingInput := header + "." + payload
	mac := hmac.New(sha256.New, jwtSecret)
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + sig
}

// buildMux registers a realistic route mix: static, 1-param, 3-param,
// catch-all, a ThrottlePerIP-guarded route (global table mutex, CH-06/07),
// and a JWTAuth-guarded route (sync.Pool of hmac.Hash, CH-10) — see
// contention-hunt.md for the finding IDs.
func buildMux() *muxmaster.Mux {
	m := muxmaster.New()
	m.Pre(middleware.RequestID())
	m.Use(middleware.RecovererWithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))), middleware.Logger(io.Discard))

	m.GET("/static/list", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	m.GET("/users/:id", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(muxmaster.PathParam(r, "id")))
	})
	m.GET("/users/:id/posts/:postID/comments/:cid", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(muxmaster.PathParam(r, "cid")))
	})
	m.GET("/static/assets/*filepath", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(muxmaster.PathParam(r, "filepath")))
	})

	throttled := m.With(middleware.ThrottlePerIP(200, time.Second, func(r *http.Request) string {
		// Fixed small keyspace (16 shards) — realistic per-client-shard load,
		// deliberately NOT one-key-per-request. See the isolated
		// BenchmarkThrottlePerIPSingleKey / ManyKeys microbenchmarks for the
		// controlled comparison.
		return r.Header.Get("X-Shard")
	}))
	throttled.GET("/throttled/:id", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(muxmaster.PathParam(r, "id")))
	})

	secure := m.With(middleware.JWTAuth(middleware.JWTOptions{
		Secret:     jwtSecret,
		Algorithms: []string{"HS256"},
	}))
	secure.GET("/secure/:id", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(muxmaster.PathParam(r, "id")))
	})

	return m
}

// runServer starts the MuxMaster-backed HTTP server, prints its listen
// address on the first stdout line (so a driver script can capture it),
// waits for -profduration (giving the client process time to complete its
// load window), then dumps mutex/block/CPU profiles and exits. Profiling is
// enabled for the process's ENTIRE lifetime — including connection setup —
// so the sweep is not biased toward steady state only.
func runServer(args []string) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:0", "listen address")
	profDuration := fs.Duration("profduration", 6*time.Second, "how long to run before dumping profiles and exiting")
	tag := fs.String("tag", "0", "suffix for profile filenames (e.g. the connection count under test)")
	profDir := fs.String("profdir", "../profiles", "directory to write profiles into")
	_ = fs.Parse(args[1:])

	runtime.SetMutexProfileFraction(1)
	runtime.SetBlockProfileRate(1000) // sample every 1000ns of blocking — full rate is too costly under 10k conns

	mux := buildMux()
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()

	// First stdout line is the address — the client/driver reads it.
	fmt.Println(ln.Addr().String())

	cpuF, _ := os.Create(*profDir + "/loadgen-server-cpu-" + *tag + ".pb.gz")
	if cpuF != nil {
		_ = pprof.StartCPUProfile(cpuF)
	}

	time.Sleep(*profDuration)

	if cpuF != nil {
		pprof.StopCPUProfile()
		_ = cpuF.Close()
	}
	if mf, err := os.Create(*profDir + "/loadgen-server-mutex-" + *tag + ".pb.gz"); err == nil {
		_ = pprof.Lookup("mutex").WriteTo(mf, 0)
		_ = mf.Close()
	}
	if bf, err := os.Create(*profDir + "/loadgen-server-block-" + *tag + ".pb.gz"); err == nil {
		_ = pprof.Lookup("block").WriteTo(bf, 0)
		_ = bf.Close()
	}
	if gf, err := os.Create(*profDir + "/loadgen-server-goroutine-" + *tag + ".pb.gz"); err == nil {
		_ = pprof.Lookup("goroutine").WriteTo(gf, 0)
		_ = gf.Close()
	}

	_ = srv.Close()
}
